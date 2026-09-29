package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinPromptsKeepTheMarkersTheEngineParses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	for role, markers := range map[string][]string{
		"groom":     {"`question:`", "`contract:`", "(recommended)", "`N. <option>`"},
		"judge":     {"`approve:`", "`reject:`", "`pass <k>: <evidence>`", "`fail <k>:", "`manual <k>`"},
		"reconcile": {"`valid:`", "`stale:", "`done:"},
		"worker":    {"Do not commit, push or create branches", "{checks}"},
	} {
		text := rolePrompt(role)
		for _, m := range markers {
			if !strings.Contains(text, m) {
				t.Errorf("%s prompt lost %s, so the engine cannot read the answer", role, m)
			}
		}
		if !strings.Contains(text, "<untrusted_content>") || !strings.Contains(text, "ASD-STE100") {
			t.Errorf("%s prompt is missing the shared rules", role)
		}
	}
}

func TestGroomPromptListsContractSectionsInOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	text := rolePrompt("groom")
	last := -1
	for _, section := range []string{"## Problem", "## Change", "## Commit", "## Acceptance", "## Files", "## Assumptions"} {
		i := strings.Index(text, "`"+section+"`")
		if i < 0 || i < last {
			t.Fatalf("section %s is missing or out of order, so contracts would not parse", section)
		}
		last = i
	}
	for _, want := range []string{"`type(scope): description`", "72 characters", "`feat`, `fix`, `refactor`, `docs`, `test`, `chore`"} {
		if !strings.Contains(text, want) {
			t.Errorf("groom prompt is missing %q from the commit rule", want)
		}
	}
}

func TestPromptOverrideReplacesOnlyItsRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	dir := filepath.Join(home, ".ghafk", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "judge.md"), []byte("Custom judge. Answer approve: or reject:."), 0o644); err != nil {
		t.Fatal(err)
	}
	judge := rolePrompt("judge")
	if !strings.HasPrefix(judge, "Custom judge.") || strings.Contains(judge, "Another agent wrote the") {
		t.Fatalf("override did not replace the built-in judge prompt:\n%s", judge)
	}
	if !strings.Contains(judge, "<untrusted_content>") {
		t.Fatal("an override dropped the shared rules")
	}
	if groom := rolePrompt("groom"); !strings.Contains(groom, "`contract:`") {
		t.Fatal("a judge override changed the groom prompt")
	}
}

func TestWorkerPromptNamesTheRepositoryChecks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	got := workerPrompt(workflow{checks: "go test ./..."})
	if !strings.Contains(got, "these checks pass: `go test ./...`") || strings.Contains(got, "{checks}") {
		t.Fatalf("worker prompt does not name the checks command:\n%s", got)
	}
}
