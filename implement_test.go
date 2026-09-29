package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func gitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"commit", "-q", "--allow-empty", "-m", "base"}} {
		if _, err := run(dir, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := run(dir, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return dir, head
}

func TestWorkChangesSeesEditsAndWorkerCommits(t *testing.T) {
	dir, base := gitRepo(t)
	if _, changed, err := workChanges(dir, base); err != nil || changed {
		t.Fatalf("untouched worktree: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, changed, err := workChanges(dir, base)
	if err != nil || !changed || status == "" {
		t.Fatalf("uncommitted edit: status=%q changed=%v err=%v", status, changed, err)
	}
	if _, err := run(dir, "git", "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "commit", "-m", "feat: add a"); err != nil {
		t.Fatal(err)
	}
	status, changed, err = workChanges(dir, base)
	if err != nil || !changed || status != "" {
		t.Fatalf("a commit made by the worker itself still counts as a change: status=%q changed=%v err=%v", status, changed, err)
	}
}

func TestSquashFoldsWorkerCommitsIntoOne(t *testing.T) {
	dir, base := gitRepo(t)
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := run(dir, "git", "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := run(dir, "git", "commit", "-m", "worker "+name); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := squashOnto(dir, base, "feat: add a", "Closes #5")
	if err != nil || !committed {
		t.Fatalf("squash failed: %v %v", committed, err)
	}
	if count, _ := run(dir, "git", "rev-list", "--count", base+"..HEAD"); count != "1" {
		t.Fatalf("want one commit on top of the base, got %s", count)
	}
	if msg, _ := run(dir, "git", "log", "-1", "--format=%B"); msg != "feat: add a\n\nCloses #5" {
		t.Fatalf("commit message %q", msg)
	}
	if files, _ := run(dir, "git", "diff", "--name-only", base, "HEAD"); files != "a.txt\nb.txt\nc.txt" {
		t.Fatalf("the squashed commit lost changes: %q", files)
	}
}

func TestSquashOfNoNetChangeMakesNoCommit(t *testing.T) {
	dir, base := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "commit", "-m", "add"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "rm", "-q", "a.txt"); err != nil {
		t.Fatal(err)
	}
	committed, err := squashOnto(dir, base, "feat: nothing")
	if err != nil || committed {
		t.Fatalf("a change that nets to nothing must not commit: %v %v", committed, err)
	}
	if head, _ := run(dir, "git", "rev-parse", "HEAD"); head != base {
		t.Fatal("HEAD must stay at the base")
	}
}

func gitClone(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	if _, err := run(origin, "git", "init", "-q", "--bare", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	src, _ := gitRepo(t)
	for _, args := range [][]string{{"branch", "-M", "main"}, {"remote", "add", "origin", origin}, {"push", "-q", "origin", "main"}} {
		if _, err := run(src, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func runIssueWith(t *testing.T, repo, worker string) []ghCall {
	t.Helper()
	needSandbox(t)
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "repo view") {
			return "main", nil
		}
		return "", nil
	})
	wf := workflow{label: "agent", worker: harness.Role{Command: worker}, timeout: time.Minute}
	if err := runIssue(t.TempDir(), repo, "demo", "owner", wf, "## Change\n\nx", issue{Number: 5, Title: "add x"}); err != nil {
		t.Fatalf("runIssue returned an error, so the next tick runs the worker again: %v", err)
	}
	return *calls
}

func TestWorkerWithoutChangesParks(t *testing.T) {
	calls := runIssueWith(t, gitClone(t), "true")
	if !called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a worker that changed nothing was requeued instead of parked, so it reruns every tick: %v", calls)
	}
}

func TestRejectedPushParks(t *testing.T) {
	repo := gitClone(t)
	for _, args := range [][]string{{"checkout", "-q", "-b", "agent/5"}, {"commit", "-q", "--allow-empty", "-m", "left over"}, {"push", "-q", "origin", "agent/5"}, {"checkout", "-q", "main"}, {"branch", "-q", "-D", "agent/5"}} {
		if _, err := run(repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	calls := runIssueWith(t, repo, "echo x > x.txt")
	if !called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a rejected push was not parked, so the worker reruns every tick: %v", calls)
	}
}

func TestAWorkerThatBreaksItsWorktreeParks(t *testing.T) {
	calls := runIssueWith(t, gitClone(t), `rm -rf "$PWD"`)
	if !called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a worker that removed its worktree was left queued, so it runs again every tick: %v", calls)
	}
}

func TestAnEmptyContractParksInsteadOfGroomingAgain(t *testing.T) {
	repo := gitClone(t)
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "repo view") {
			return "main", nil
		}
		return "", nil
	})
	wf := workflow{label: "agent", groomer: harness.Role{Command: `printf 'contract:\n'`}, timeout: time.Minute}
	if err := groomIssue(t.TempDir(), repo, "demo", "owner", wf, issue{Number: 5, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if !called(*calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("an empty contract was saved and queued, so the groomer runs again every tick: %v", *calls)
	}
}

func TestAWorkflowChangeIsNeverPushed(t *testing.T) {
	needSandbox(t)
	repo := gitClone(t)
	calls := runIssueWith(t, repo, `mkdir -p .github/workflows && echo "name: x" > .github/workflows/x.yml`)
	if !called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a worker's workflow change was not handed to the owner: %v", calls)
	}
	if remote, _ := run(repo, "git", "ls-remote", "--heads", "origin", "agent/5"); remote != "" {
		t.Fatal("the workflow change was pushed, so GitHub Actions runs it with the repository's secrets")
	}
	body := ""
	for _, c := range calls {
		body += c.String()
	}
	if !strings.Contains(body, "name: x") {
		t.Fatal("the park comment does not show the held workflow change")
	}
}

func TestAnOrdinaryChangeIsStillPushed(t *testing.T) {
	needSandbox(t)
	repo := gitClone(t)
	runIssueWith(t, repo, `mkdir -p docs && echo x > docs/github.md`)
	if remote, _ := run(repo, "git", "ls-remote", "--heads", "origin", "agent/5"); remote == "" {
		t.Fatal("a change outside .github/ was not pushed")
	}
}

func TestAContractWithASecretIsStoredRedacted(t *testing.T) {
	needSandbox(t)
	resetSecretRegistry(t)
	repo := gitClone(t)
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "repo view") {
			return "main", nil
		}
		return "", nil
	})
	token := dummyGitHubToken()
	wf := workflow{label: "agent", groomer: harness.Role{Command: `printf 'contract:\nUse ` + token + ` to call the API.\n'`}, timeout: time.Minute}
	if err := groomIssue(t.TempDir(), repo, "demo", "owner", wf, issue{Number: 5, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	stored := false
	for _, c := range *calls {
		for _, arg := range c.args {
			st, ok := parseCard(strings.TrimPrefix(arg, "body="))
			if !ok || st.Contract == "" {
				continue
			}
			stored = true
			if strings.Contains(st.Contract, token) || !strings.Contains(st.Contract, secretPlaceholder) {
				t.Fatalf("the card state kept the secret: %q", st.Contract)
			}
		}
	}
	if !stored {
		t.Fatalf("no card with the contract was written: %v", *calls)
	}
}

func TestGroomingRecordsTheIssueTextItWasGiven(t *testing.T) {
	needSandbox(t)
	repo := gitClone(t)
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "repo view") {
			return "main", nil
		}
		return "", nil
	})
	is := issue{Number: 5, Title: "t", Body: "the text"}
	wf := workflow{label: "agent", groomer: harness.Role{Command: `printf 'contract:\n## Change\ndo it\n'`}, timeout: time.Minute}
	if err := groomIssue(t.TempDir(), repo, "demo", "owner", wf, is); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		for _, arg := range c.args {
			if st, ok := parseCard(strings.TrimPrefix(arg, "body=")); ok && st.Contract != "" {
				if st.Groomed != issueDigest(is) {
					t.Fatalf("the card must record the issue text the contract came from, got %q", st.Groomed)
				}
				return
			}
		}
	}
	t.Fatalf("no card with the contract was written: %v", *calls)
}
