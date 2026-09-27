package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEngineEnv(t *testing.T, text string, mode os.FileMode) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ghafk"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ghafk", "env"), []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	return home
}

func useOwnerToken(t *testing.T) {
	t.Helper()
	old := ownerToken
	t.Cleanup(func() { ownerToken = old })
}

func TestEngineEnvGivesTheTokenOnlyToGhafksOwnCalls(t *testing.T) {
	useOwnerToken(t)
	t.Setenv("GHAFK_TEST_KEY", "")
	if err := loadEngineEnv(writeEngineEnv(t, "# engine\nGH_TOKEN=secret-token\nGHAFK_TEST_KEY=harness-key\n", 0o600)); err != nil {
		t.Fatal(err)
	}
	calls := fakeGH(t, nil)
	if _, err := ghOwner(".", "api", "user"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join((*calls)[0].env, "\n"), "GH_TOKEN=secret-token") {
		t.Fatal("ghafk's own gh call did not get the token from the env file")
	}
	agent := strings.Join(runEnv(), "\n")
	if strings.Contains(agent, "secret-token") {
		t.Fatal("the environment for agents and checks carries the owner token")
	}
	if !strings.Contains(agent, "GHAFK_TEST_KEY=harness-key") {
		t.Fatal("a harness key from the env file did not reach the agent environment")
	}
}

func TestEngineEnvMustBeOwnerOnly(t *testing.T) {
	useOwnerToken(t)
	if err := loadEngineEnv(writeEngineEnv(t, "GH_TOKEN=x\n", 0o640)); err == nil {
		t.Fatal("a group-readable env file was accepted, so other accounts can read the token")
	}
}

func TestEngineEnvRejectsMalformedLines(t *testing.T) {
	useOwnerToken(t)
	for _, text := range []string{"export GH_TOKEN=x\n", "gh_token=x\n", "GH_TOKEN\n"} {
		if err := loadEngineEnv(writeEngineEnv(t, text, 0o600)); err == nil {
			t.Errorf("%q was accepted", strings.TrimSpace(text))
		}
	}
}

func TestGitGetsTheOwnerTokenForPushes(t *testing.T) {
	useOwnerToken(t)
	ownerToken = "secret-token"
	dir, _ := gitRepo(t)
	script := filepath.Join(t.TempDir(), "show")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf %s \"$GH_TOKEN\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := run(dir, "git", "-c", "alias.showtoken=!"+script, "showtoken")
	if err != nil || got != "secret-token" {
		t.Fatalf("git saw GH_TOKEN %q, %v; the gh credential helper cannot push", got, err)
	}
}
