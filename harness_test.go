package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestResolveWorkerProfileTemplate(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: claude opus\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	want := "claude -p --output-format json --model opus --permission-mode acceptEdits --allowedTools Bash"
	if wf.worker.Command != want {
		t.Fatalf("command = %q, want %q", wf.worker.Command, want)
	}
	if wf.worker.Parser != "claude-json" {
		t.Fatalf("parser = %q, want claude-json", wf.worker.Parser)
	}
	if wf.worker.Label != "claude opus" {
		t.Fatalf("label = %q, want claude opus", wf.worker.Label)
	}
}

func TestWorkerModelIsValidated(t *testing.T) {
	_, err := parseWorkflow("---\nworker: claude opus; rm -rf ~\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err == nil {
		t.Fatal("a model with shell metacharacters must be refused")
	}
	if !strings.Contains(err.Error(), "opus; rm -rf ~") {
		t.Fatalf("err = %v, want it to name the model", err)
	}
}

func TestDefaultProfileFromConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".ghafk", "config")
	if err := os.WriteFile(config, []byte("budget: 5\ndefault: pi gpt-5.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	henv, err := loadHarnessEnv()
	if err != nil {
		t.Fatal(err)
	}
	wf, err := parseWorkflow("---\nlabel: agent\n---\n", henv)
	if err != nil {
		t.Fatal(err)
	}
	want := "pi -p --no-session --mode json --model gpt-5.2"
	if wf.worker.Command != want || wf.worker.Label != "pi gpt-5.2" {
		t.Fatalf("worker = %#v, want the config default", wf.worker)
	}
	if wf.groomer.Command != want || wf.judge.Command != want || wf.reconciler.Command != want {
		t.Fatal("the other roles must inherit the default worker")
	}
}

func TestRawWorkerValueStaysARawCommand(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: worker/run model-a\n---\n", harness.Env{Profiles: harness.Builtins()})
	if err != nil {
		t.Fatal(err)
	}
	if wf.worker.Command != "worker/run model-a" || wf.worker.Parser != "" || wf.worker.Label != "command" {
		t.Fatalf("worker = %#v, want the raw command with no parser", wf.worker)
	}
}

func labelTestWorkflow(t *testing.T, harnessFile string) workflow {
	t.Helper()
	henv := harness.Env{Profiles: harness.Builtins()}
	if harnessFile != "" {
		file := filepath.Join(t.TempDir(), "harnesses")
		if err := os.WriteFile(file, []byte(harnessFile), 0o644); err != nil {
			t.Fatal(err)
		}
		var err error
		if henv, err = harness.LoadEnv(filepath.Dir(file)); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := parseWorkflow("---\nworker: pi model-a\njudge: claude opus\n---\n", henv)
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestWorkerForLabels(t *testing.T) {
	wf := labelTestWorkflow(t, "claude.allow: opus sonnet\n")
	cases := []struct {
		name      string
		labels    []label
		wantLabel string
		wantErr   string
	}{
		{"no labels", nil, "pi model-a", ""},
		{"harness label alone", []label{{Name: "harness:claude"}}, "claude model-a", ""},
		{"model label alone", []label{{Name: "model:sonnet"}}, "pi sonnet", ""},
		{"both labels", []label{{Name: "harness:claude"}, {Name: "model:opus"}}, "claude opus", ""},
		{"unknown profile", []label{{Name: "harness:nosuch"}}, "", "harness:nosuch"},
		{"model with a space", []label{{Name: "model:son net"}}, "", "model:son net"},
		{"model outside the allow list", []label{{Name: "harness:claude"}, {Name: "model:haiku"}}, "", "does not allow"},
		{"two model labels", []label{{Name: "model:opus"}, {Name: "model:sonnet"}}, "", "exactly one"},
	}
	for _, tc := range cases {
		r, err := workerForLabels(wf, tc.labels)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if r.Label != tc.wantLabel {
			t.Errorf("%s: label = %q, want %q", tc.name, r.Label, tc.wantLabel)
		}
	}
}

func TestHarnessLabelLeavesTheJudgeConfigured(t *testing.T) {
	wf := labelTestWorkflow(t, "")
	r, err := workerForLabels(wf, []label{{Name: "harness:codex"}})
	if err != nil {
		t.Fatal(err)
	}
	wantWorker := "codex exec --json --skip-git-repo-check --sandbox workspace-write --model model-a -"
	if r.Command != wantWorker {
		t.Fatalf("worker command = %q, want %q", r.Command, wantWorker)
	}
	wantJudge := "claude -p --output-format json --model opus --permission-mode acceptEdits --allowedTools Bash"
	if wf.judge.Command != wantJudge {
		t.Fatalf("judge command = %q, want the WORKFLOW.md one %q", wf.judge.Command, wantJudge)
	}
}
