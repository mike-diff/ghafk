package main

import (
	"strings"
	"testing"
	"time"
)

func TestEngineUnitRunsTheRootOwnedBinaryAsTheEngineInsideTheSandbox(t *testing.T) {
	unit := engineServiceUnit(linuxEngine)
	for _, want := range []string{"User=ghafk", "Group=ghafk", "ExecStart=/usr/local/bin/ghafk tick", "ProtectHome=true", "ProtectSystem=strict", "ReadWritePaths=/var/lib/ghafk", "NoNewPrivileges=yes", "Environment=PATH=/var/lib/ghafk/.local/bin:"} {
		if !strings.Contains(unit, want+"\n") && !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "EnvironmentFile") || strings.Contains(unit, "GH_TOKEN") {
		t.Errorf("the unit carries the token, which `systemctl show` prints to any user:\n%s", unit)
	}
}

func TestHarnessCheckRunsUnderTheSameSandboxAsTheEngine(t *testing.T) {
	args := strings.Join(sandboxArgs(linuxEngine, "/usr/local/bin/ghafk", "harness", "list"), "\x00")
	for _, p := range engineHardening(linuxEngine.home) {
		if !strings.Contains(args, "-p\x00"+p+"\x00") {
			t.Errorf("the harness check lacks %q, so it can pass where the engine fails", p)
		}
	}
	if !strings.Contains(args, "--uid=ghafk") || !strings.HasSuffix(args, "/usr/local/bin/ghafk\x00harness\x00list") {
		t.Errorf("sandbox args = %q", args)
	}
}

func TestEngineEnvRewriteReplacesOnlyTheToken(t *testing.T) {
	got := renderEngineEnv("GH_TOKEN=old\nANTHROPIC_API_KEY=k\n\n", "new")
	if got != "GH_TOKEN=new\nANTHROPIC_API_KEY=k\n" {
		t.Fatalf("env = %q; a token change must keep the harness keys", got)
	}
}

func TestEngineGitconfigPushesThroughGh(t *testing.T) {
	cfg := renderEngineGitconfig("Mike", "m@example.com", "/usr/bin/gh")
	for _, want := range []string{"name = Mike", "email = m@example.com", "helper = !/usr/bin/gh auth git-credential"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("gitconfig lacks %q:\n%s", want, cfg)
		}
	}
}

func TestHarnessesOnPathReadsTheHarnessList(t *testing.T) {
	list := "claude  claude-json  claude -p  on PATH\npi  pi-json  pi -p  not on PATH\ncodex  codex-json  codex exec  on PATH\ndefault: (none)"
	if got := strings.Join(harnessesOnPath(list), ","); got != "claude,codex" {
		t.Fatalf("found %q", got)
	}
}

func TestEngineOutputCannotDriveThePersonsTerminal(t *testing.T) {
	if got := sanitizeTerminal("ok\x1b]0;title\x07\x1b[2Jdone\n\tnext"); got != "ok]0;title[2Jdone\n\tnext" {
		t.Fatalf("escape sequences from the engine reached the terminal: %q", got)
	}
}

func TestEngineStatusWarnsAboutAMissingHarnessAndAnExpiringToken(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	out := renderEngineStatus(engineStatus{Time: "2026-10-20T00:00:00Z", Error: "gh: Bad credentials", Owner: "mike", TokenExpires: "2026-10-27T00:00:00Z", Harnesses: []string{}}, now)
	for _, want := range []string{"last tick failed: gh: Bad credentials", "expires in 7 days", "no harness"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestTokenURLAsksForTheNeededPermissions(t *testing.T) {
	u := tokenURL()
	for _, want := range []string{"contents=write", "issues=write", "pull_requests=write", "expires_in=366"} {
		if !strings.Contains(u, want) {
			t.Errorf("token URL lacks %q: %s", want, u)
		}
	}
}
