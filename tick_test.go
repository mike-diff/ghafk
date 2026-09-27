package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	repo, _ := gitRepo(t)
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
