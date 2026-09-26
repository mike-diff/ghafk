package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoStateNamesTheProblem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	if got := repoState(repo); !strings.Contains(got, "WORKFLOW.md") {
		t.Fatalf("a repository without a workflow file shows %q; it must say what is missing", got)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".ghafk", "WORKFLOW.md"), []byte("---\nlabel: agent\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repoState(repo); !strings.Contains(got, "no worker") {
		t.Fatalf("a workflow with no worker and no machine default shows %q; it must name the cause", got)
	}
}

func TestUsageNamesEveryCommand(t *testing.T) {
	for _, verb := range []string{"tick", "start", "stop", "status", "init", "remove", "harness", "help"} {
		if !strings.Contains(usageText, "ghafk "+verb) {
			t.Errorf("usage does not describe %q", verb)
		}
	}
}
