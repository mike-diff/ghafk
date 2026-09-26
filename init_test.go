package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestGuessChecksReturnsTheGoCommandUnderGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -count=1 ./...`
	if got := guessChecks(dir); got != want {
		t.Fatalf("go.mod: got %q, want %q", got, want)
	}
	if got := guessChecks(t.TempDir()); got != "" {
		t.Fatalf("empty directory: got %q, want no checks", got)
	}
}

func TestWorkflowTextLeavesTheWorkerToTheMachineDefault(t *testing.T) {
	text := workflowText("go test ./...")
	if strings.Contains(text, "worker:") {
		t.Fatalf("new repos get a hardcoded worker, so every user inherits one model:\n%s", text)
	}
	wf, err := parseWorkflow(text, harness.Env{Profiles: harness.Builtins(), Default: "claude sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if wf.worker.Label != "claude sonnet" || wf.worker.Parser != "claude-json" {
		t.Fatalf("worker %q with parser %q, want the machine default profile", wf.worker.Label, wf.worker.Parser)
	}
}

func TestHarnessLabelPlans(t *testing.T) {
	onPath := harnessScript(t, "true\n")
	profiles := map[string]harness.Profile{
		"here":   {Parser: "text", Command: onPath},
		"absent": {Parser: "text", Command: filepath.Join(t.TempDir(), "no-such-worker")},
	}
	got := plannedHarnessLabels(profiles)
	if len(got) != 1 || got[0] != "harness:here" {
		t.Fatalf("planned = %v, want [harness:here]", got)
	}
}

func TestAddRepoLeavesTheFileUnchangedForAPathAlreadyPresent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "repos")
	want := "# one repo per line\n/home/me/demo\n"
	if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addRepo(file, "/home/me/demo"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("repos = %q, want unchanged %q", got, want)
	}
}

func TestDropRepoLineKeepsCommentsAndBlanks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "repos")
	if err := os.WriteFile(file, []byte("# one repo per line\n\n/home/me/demo\n/home/me/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRepoLine(file, "/home/me/demo"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# one repo per line\n\n/home/me/other\n"; string(got) != want {
		t.Fatalf("repos = %q, want %q", got, want)
	}
}

func TestGuessChecksFollowsTheLockfile(t *testing.T) {
	cases := map[string]string{
		"pnpm-lock.yaml":    "pnpm install --frozen-lockfile && pnpm test",
		"yarn.lock":         "yarn install --frozen-lockfile && yarn test",
		"package-lock.json": "npm ci && npm test",
	}
	for lock, want := range cases {
		dir := t.TempDir()
		for _, f := range []string{"package.json", lock} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := guessChecks(dir); got != want {
			t.Errorf("with %s: got %q, want %q", lock, got, want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guessChecks(dir); got != "cargo test" {
		t.Errorf("Rust project: got %q, want cargo test", got)
	}
}
