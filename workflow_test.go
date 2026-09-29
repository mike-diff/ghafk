package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestParseWorkflowReconcilerDefaultsToGroomer(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: w\ngroomer: g\n---\nbody", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.reconciler.Command != "g" {
		t.Fatalf("reconciler = %q, want the groomer", wf.reconciler.Command)
	}
	if wf.judge.Command != "w" {
		t.Fatalf("judge = %q, want the worker", wf.judge.Command)
	}
}

func TestParseWorkflowKeepsColonsInsideAValue(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: w\nchecks: sh -c 'echo a:b'\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.checks != "sh -c 'echo a:b'" {
		t.Fatalf("checks = %q", wf.checks)
	}
}

func TestParseWorkflowBodyExcludesFrontMatter(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: w\n---\n\nRead the map.\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.body != "Read the map." {
		t.Fatalf("body = %q", wf.body)
	}
}

func TestParseWorkflowRejectsMissingWorker(t *testing.T) {
	if _, err := parseWorkflow("---\nlabel: agent\n---\n", harness.Env{Profiles: harness.Builtins()}); err == nil {
		t.Fatal("expected an error for a workflow with no worker")
	}
}

func TestParseWorkflowReadsTimeoutInMinutes(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: w\ntimeout: 2\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.timeout != 2*time.Minute {
		t.Fatalf("timeout = %v, want 2m0s", wf.timeout)
	}
}

func TestParseWorkflowDefaultsTimeoutToThirtyMinutes(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: w\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.timeout != 30*time.Minute {
		t.Fatalf("timeout = %v, want 30m0s", wf.timeout)
	}
}

func TestWorkflowComesFromTheDefaultBranchOnGitHub(t *testing.T) {
	src := gitClone(t)
	fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "repo view") {
			return "main", nil
		}
		return "", nil
	})
	origin, err := run(src, "git", "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(t.TempDir(), "engine")
	if _, err := run(t.TempDir(), "git", "clone", "-q", origin, engine); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".ghafk", "WORKFLOW.md"), []byte("---\nchecks: go test ./...\nworker: w\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "workflow"}, {"push", "-q", "origin", "main"}} {
		if _, err := run(src, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(engine, "git", "fetch", "-q", "origin"); err != nil {
		t.Fatal(err)
	}
	wf, err := loadWorkflow(engine, harness.Env{})
	if err != nil || wf.checks != "go test ./..." {
		t.Fatalf("checks = %q, %v; the engine's clone kept an old workflow after a change merged on GitHub", wf.checks, err)
	}
}

func TestARunTimeoutStaysBelowTheStaleRunSweep(t *testing.T) {
	env := harness.Env{Profiles: harness.Builtins()}
	if _, err := parseWorkflow("---\nworker: w\ntimeout: 720\n---\n", env); err != nil {
		t.Fatalf("a 12 hour timeout must be accepted: %v", err)
	}
	if _, err := parseWorkflow("---\nworker: w\ntimeout: 1500\n---\n", env); err == nil {
		t.Fatal("a timeout longer than the stale-run sweep must be refused")
	}
	if time.Duration(maxTimeoutMinutes)*time.Minute >= staleRunAge {
		t.Fatal("the longest run must be shorter than the age at which run folders are swept")
	}
}

func TestTheWorkingTreeFallbackIgnoresEgressAndSecretsAllow(t *testing.T) {
	fakeGH(t, func(string) (string, error) { return "", errors.New("offline") })
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nworker: w\nchecks: true\negress: agent-chosen.example.com\nsecrets-allow: leak/\n---\n"
	if err := os.WriteFile(filepath.Join(repo, ".ghafk", "WORKFLOW.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := loadWorkflow(repo, harness.Env{})
	if err != nil || wf.checks != "true" {
		t.Fatalf("the fallback must still read the workflow: %v %v", wf.checks, err)
	}
	if len(wf.egress) != 0 || len(wf.secretsAllow) != 0 {
		t.Fatalf("a working-tree workflow widened egress or secrets-allow: %v %v", wf.egress, wf.secretsAllow)
	}
}
