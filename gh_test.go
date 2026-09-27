package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentIssueReadsOnlyAgentBranches(t *testing.T) {
	for branch, want := range map[string]int{"agent/12": 12, "agent/0": 0, "agent/x": 0, "feature/12": 0, "12": 0, "": 0} {
		if got := agentIssue(pr{HeadRefName: branch}); got != want {
			t.Errorf("agentIssue(%q) = %d, want %d", branch, got, want)
		}
	}
}

func TestIssueOpenAcceptsGitHubCase(t *testing.T) {
	for state, want := range map[string]bool{"OPEN": true, "open": true, "CLOSED": false, "": false} {
		if got := issueOpen(state); got != want {
			t.Errorf("issueOpen(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestEngineEnvCarriesTheEngineTokenWhileRunShellStripsIt(t *testing.T) {
	engineToken = "engine-secret"
	defer func() { engineToken = "" }()
	t.Setenv("GH_TOKEN", "inherited-secret")
	seen := ""
	for _, kv := range ghEnv() {
		if strings.HasPrefix(kv, "GH_TOKEN=") {
			seen = kv
		}
	}
	if seen != "GH_TOKEN=engine-secret" {
		t.Fatalf("engine gh env carries %q, want the engine token", seen)
	}
	var out bytes.Buffer
	if err := runShell("probe", t.TempDir(), `if [ -n "$GH_TOKEN" ]; then echo "$GH_TOKEN"; else echo none; fi`, "", time.Minute, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "none" {
		t.Fatalf("runShell leaked GH_TOKEN to its child: %q", got)
	}
}

func TestAnOldAppTokenIsMintedAgain(t *testing.T) {
	oldToken, oldAt, oldRemint := engineToken, engineMintedAt, engineRemint
	t.Cleanup(func() { engineToken, engineMintedAt, engineRemint = oldToken, oldAt, oldRemint })
	engineToken, engineMintedAt = "expiring", time.Now().Add(-51*time.Minute)
	engineRemint = func() (string, error) { return "fresh", nil }
	env := strings.Join(ghEnv(), "\n")
	if !strings.Contains(env, "GH_TOKEN=fresh") || strings.Contains(env, "GH_TOKEN=expiring") {
		t.Fatal("a tick longer than an hour kept using an expired app token, so its later bot calls fail")
	}
}
