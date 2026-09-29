package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestPickIssueSkipsIssuesHeldThisTick(t *testing.T) {
	issues := []issue{{Number: 61}, {Number: 64}, {Number: 70}}
	if got := pickIssue(issues, map[int]bool{64: true}, map[int]bool{61: true}); got != 2 {
		t.Fatalf("picked index %d, want 2: #61 was stopped this tick and #64 has an open PR", got)
	}
	if got := pickIssue(issues, nil, nil); got != 0 {
		t.Fatalf("picked index %d, want the lowest number", got)
	}
}

func floodedComments() string {
	var cs []string
	for i := 0; i < commentLimit; i++ {
		cs = append(cs, `{"body":"spam","author":{"login":"outsider"},"authorAssociation":"NONE"}`)
	}
	return "[" + strings.Join(cs, ",") + "]"
}

func TestAFloodedIssueParksInsteadOfGrooming(t *testing.T) {
	repo := gitClone(t)
	if err := os.MkdirAll(filepath.Join(repo, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".ghafk", "WORKFLOW.md"), []byte("---\nlabel: agent\nchecks: true\ngroomer: false\nworker: false\njudge: false\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flooded := `[{"number":5,"author":{"login":"owner"},"title":"t","body":"b","labels":[{"name":"agent"}],"comments":` + floodedComments() + `}]`
	calls := fakeGH(t, func(cmd string) (string, error) {
		switch {
		case strings.HasPrefix(cmd, "issue list"):
			return flooded, nil
		case strings.HasPrefix(cmd, "pr list"):
			return "[]", nil
		case strings.Contains(cmd, "/permission"):
			return "admin", nil
		}
		return "", nil
	})
	if err := workRepo(t.TempDir(), repo, "owner", harness.Env{}); err != nil {
		t.Fatal(err)
	}
	if !called(*calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("an issue with more comments than gh returns was worked blind: %v", *calls)
	}
}

func TestAFloodedPullRequestParks(t *testing.T) {
	stubWriters(t, "owner")
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "issue view 5") {
			return `{"number":5,"author":{"login":"owner"},"title":"t","body":"b","comments":[],"state":"OPEN","labels":[{"name":"agent"}]}`, nil
		}
		return "", nil
	})
	var flood []prComment
	if err := json.Unmarshal([]byte(floodedComments()), &flood); err != nil {
		t.Fatal(err)
	}
	prs := []pr{{Number: 7, HeadRefName: "agent/5", Comments: flood}}
	if handled, err := workPR(t.TempDir(), t.TempDir(), "repo", "owner", workflow{label: "agent", checks: "true"}, prs); err != nil || !handled {
		t.Fatalf("workPR = %v, %v", handled, err)
	}
	if !called(*calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a PR with more comments than gh returns was worked blind: %v", *calls)
	}
}

func tickWithGroomedCard(t *testing.T, groomedBody, currentBody string) []ghCall {
	t.Helper()
	needSandbox(t)
	repo := gitClone(t)
	if err := os.MkdirAll(filepath.Join(repo, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	flow := "---\nlabel: agent\nchecks: true\ngroomer: echo groomer-ran; false\nworker: echo worker-ran; false\njudge: false\n---\n"
	if err := os.WriteFile(filepath.Join(repo, ".ghafk", "WORKFLOW.md"), []byte(flow), 0o644); err != nil {
		t.Fatal(err)
	}
	groomed := issue{Title: "t", Body: groomedBody}
	st := cardState{Number: 5, Title: "t", Phase: "queued", Contract: "## Change\nthe old contract", Groomed: issueDigest(groomed)}
	card, _ := json.Marshal(renderCardBody(st, time.UTC, false))
	issues := `[{"number":5,"author":{"login":"owner"},"title":"t","body":` + strconv.Quote(currentBody) + `,"labels":[{"name":"agent"}],"comments":[{"body":` + string(card) + `,"author":{"login":"owner"},"authorAssociation":"OWNER"}]}]`
	calls := fakeGH(t, func(cmd string) (string, error) {
		switch {
		case strings.HasPrefix(cmd, "issue list"):
			return issues, nil
		case strings.HasPrefix(cmd, "pr list"):
			return "[]", nil
		case strings.HasPrefix(cmd, "repo view"):
			return "main", nil
		case strings.Contains(cmd, "/permission"):
			return "admin", nil
		}
		return "", nil
	})
	_ = workRepo(t.TempDir(), repo, "owner", harness.Env{})
	return *calls
}

func ranRole(calls []ghCall, role string) bool {
	for _, c := range calls {
		for _, arg := range c.args {
			if strings.Contains(arg, "**ghafk** · "+role+" ·") {
				return true
			}
		}
	}
	return false
}

func TestAnIssueEditedAfterGroomingIsGroomedAgain(t *testing.T) {
	calls := tickWithGroomedCard(t, "the old text", "the new text")
	if !ranRole(calls, "groomer") || ranRole(calls, "worker") {
		t.Fatalf("an issue whose text changed after grooming must be groomed again, not built from the old contract: %v", calls)
	}
}

func TestAnUnchangedIssueKeepsItsContract(t *testing.T) {
	calls := tickWithGroomedCard(t, "the same text", "the same text")
	if !ranRole(calls, "worker") || ranRole(calls, "groomer") {
		t.Fatalf("an issue whose text did not change must go on to the worker with its contract: %v", calls)
	}
}

func TestACardFromBeforeTheDigestKeepsItsContract(t *testing.T) {
	if editedSinceGroom(issue{Title: "t", Body: "any text"}, cardState{Contract: "c"}) {
		t.Fatal("a card written before ghafk recorded the groomed text must keep its contract, not re-groom every issue in flight")
	}
}
