package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestWorkerChanged(t *testing.T) {
	if !workerChanged(" M main.go", "b", "b") {
		t.Fatal("dirty tree means changed")
	}
	if !workerChanged("", "b", "a") {
		t.Fatal("clean tree with moved HEAD means changed")
	}
	if workerChanged("", "b", "b") {
		t.Fatal("clean tree with unmoved HEAD means no changes")
	}
}

func TestBodyArgsMoveToStdin(t *testing.T) {
	big := strings.Repeat("x", 300000)
	args, stdin := bodyToStdin([]string{"issue", "comment", "7", "--body", big})
	if strings.Join(args, " ") != "issue comment 7 --body-file -" || stdin != big {
		t.Fatalf("comment: got %q with %d stdin bytes", args, len(stdin))
	}
	args, stdin = bodyToStdin([]string{"api", "-X", "PATCH", "repos/o/r/issues/comments/1", "-f", "body=" + big})
	if strings.Join(args, " ") != "api -X PATCH repos/o/r/issues/comments/1 -F body=@-" || stdin != big {
		t.Fatalf("api: got %q with %d stdin bytes", args, len(stdin))
	}
	args, stdin = bodyToStdin([]string{"pr", "merge", "3", "--subject", "fix: x", "--body", ""})
	if strings.Join(args, " ") != "pr merge 3 --subject fix: x --body-file -" || stdin != "" {
		t.Fatalf("merge: got %q", args)
	}
}

func TestErrorTextClipsLongArguments(t *testing.T) {
	_, err := runWithEnv(".", "false", nil, strings.Repeat("y", 5000))
	if err == nil || len(err.Error()) > 300 {
		t.Fatalf("error text is %d bytes, want at most 300", len(err.Error()))
	}
}

func TestRunKeepsLeadingSpacesOfTheFirstLine(t *testing.T) {
	out, err := run(".", "printf", "\n a.go | 2 +-\n b.go | 1 +\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != " a.go | 2 +-\n b.go | 1 +" {
		t.Fatalf("got %q", out)
	}
}

func TestASandboxedRunReturnsWhenAnEscapedChildHoldsItsOutput(t *testing.T) {
	needSandbox(t)
	old := pipeGrace
	pipeGrace = 100 * time.Millisecond
	defer func() { pipeGrace = old }()
	home := t.TempDir()
	var out bytes.Buffer
	start := time.Now()
	err := runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), command: "setsid sleep 30 & echo started", timeout: time.Minute, role: &harness.Role{Command: "sh"}, home: home}, &out, &out)
	if err != nil || !strings.Contains(out.String(), "started") {
		t.Fatalf("the run did not start its command: %v\n%s", err, out.String())
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("the run waited %v for a child that escaped the process group; the tick would hang", elapsed)
	}
}

func TestToolCallsHaveATimeLimit(t *testing.T) {
	old := toolTimeout
	toolTimeout = 100 * time.Millisecond
	defer func() { toolTimeout = old }()
	start := time.Now()
	if _, err := run(t.TempDir(), "sleep", "5"); err == nil {
		t.Fatal("a stuck command did not fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a stuck command ran %v; a hung git or gh call would stall every repository", elapsed)
	}
}

func TestRunEnvStripsEveryGitHubToken(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "secret-"+name)
	}
	for _, kv := range runEnv() {
		if strings.Contains(kv, "secret-") {
			t.Fatalf("worker environment still carries %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
}

func TestWorkDirsOfSameNamedReposDiffer(t *testing.T) {
	a := workDir("/home/u", "/src/a/app", "5")
	b := workDir("/home/u", "/src/b/app", "5")
	if a == b {
		t.Fatalf("two repositories named app share %s, so one can delete the other's work", a)
	}
	if filepath.Dir(filepath.Dir(a)) != "/home/u/.ghafk/work" {
		t.Fatalf("work dir %s is not one level under ~/.ghafk/work", a)
	}
	if workDir("/home/u", "/src/a/app", "5") != a {
		t.Fatal("a repository's work dir is not stable between ticks")
	}
}
