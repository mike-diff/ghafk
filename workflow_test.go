package main

import (
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
