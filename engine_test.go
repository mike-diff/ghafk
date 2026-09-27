package main

import (
	"fmt"
	"encoding/xml"
	"os"
	"path/filepath"
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

func TestEnginePlistRunsTheRootOwnedBinaryAsTheHiddenAccountWithoutSecrets(t *testing.T) {
	plist := enginePlist(darwinEngine, 2)
	for _, want := range []string{"<string>ghafk.engine</string>", "<string>/usr/local/bin/ghafk</string>", "<key>UserName</key>\n\t<string>_ghafk</string>", "<integer>120</integer>", "<key>Umask</key>\n\t<integer>23</integer>", "/usr/local/var/ghafk/.local/bin:"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	for _, bad := range []string{"GH_TOKEN", "ProcessType"} {
		if strings.Contains(plist, bad) {
			t.Errorf("plist contains %s; launchctl print shows plist environment to every user, and Background slows ticks", bad)
		}
	}
	if err := xml.Unmarshal([]byte(plist), new(struct{})); err != nil {
		t.Fatalf("plist is not well-formed XML: %v", err)
	}
}

func TestFreeServiceIDIsFreeAsBothUserAndGroup(t *testing.T) {
	users := "_www 70\n_taken 499\nme 501"
	groups := "staff 20\ncom.apple.access_ssh 498\n_ghafkold 497"
	id, err := freeServiceID(users, groups)
	if err != nil || id != 496 {
		t.Fatalf("id = %d, %v; want 496, the highest id below 500 unused by any user or group", id, err)
	}
}

func TestEngineTimerCatchesUpAfterDowntime(t *testing.T) {
	timer := engineTimerUnit(5)
	for _, want := range []string{"OnCalendar=*:0/5", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("%s.timer lacks %q:\n%s", engineUnitName, want, timer)
		}
	}
}

func TestSetupNeverTouchesTheEngineHomeAsRoot(t *testing.T) {
	var calls [][]string
	old := runPrivileged
	runPrivileged = func(stdin string, args ...string) (string, error) {
		calls = append(calls, args)
		return "GH_TOKEN=old\nKEY=v", nil
	}
	t.Cleanup(func() { runPrivileged = old })
	person := t.TempDir()
	for _, name := range []string{"config", "harnesses", "app", "app.pem", "prompts/worker.md"} {
		path := filepath.Join(person, ".ghafk", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncConfig(linuxEngine, person); err != nil {
		t.Fatal(err)
	}
	if err := storeToken(linuxEngine, "new"); err != nil {
		t.Fatal(err)
	}
	hasToken(linuxEngine)
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), linuxEngine.home) && (len(c) < 2 || c[0] != "-u" || c[1] != linuxEngine.user) {
			t.Errorf("root touches %q; an agent can point that path at any file on the system", strings.Join(c, " "))
		}
	}
	if len(calls) < 6 {
		t.Fatalf("only %d privileged calls; the files were not copied", len(calls))
	}
}

func TestEngineWriteReplacesALinkInsteadOfWritingThroughIt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "env")
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })
	w.WriteString("GH_TOKEN=x\n")
	w.Close()
	if err := engineWrite([]string{dest, "0600"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Fatalf("the write followed the link and changed %s", target)
	}
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("dest = %v, %v; want a regular 0600 file", info.Mode(), err)
	}
}

func TestStatusReadsOnlyRegularEngineFiles(t *testing.T) {
	link := filepath.Join(t.TempDir(), "status.json")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	if _, err := readEngineFile(link, 1<<20); err == nil {
		t.Fatal("a status file linked to a device was read, so an agent can hang `ghafk status`")
	}
}

func TestEngineConfigIsRootOwnedAndOnlyTheTokenMigrates(t *testing.T) {
	var calls []string
	var envContent string
	old := runPrivileged
	runPrivileged = func(stdin string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if args[0] == "install" && args[len(args)-1] == "/etc/ghafk/env" {
			data, _ := os.ReadFile(args[len(args)-2])
			envContent = string(data)
		}
		switch {
		case strings.HasSuffix(call, "/var/lib/ghafk/.ghafk/env"):
			return "GH_TOKEN=old\nGIT_SSH_COMMAND=planted", nil
		case strings.HasPrefix(call, "grep"), strings.HasPrefix(call, "cat /etc"):
			return "", fmt.Errorf("absent")
		}
		return "", nil
	}
	t.Cleanup(func() { runPrivileged = old })
	person := t.TempDir()
	if err := os.MkdirAll(filepath.Join(person, ".ghafk"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(person, ".ghafk", "config"), []byte("interval: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncConfig(linuxEngine, person); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"install -o root -g ghafk -m 0640 ", " /etc/ghafk/config", " /etc/ghafk/env", "-u ghafk rm -rf /var/lib/ghafk/.ghafk/env"} {
		if !strings.Contains(joined, want) {
			t.Errorf("setup never ran %q:\n%s", want, joined)
		}
	}
	for _, c := range calls {
		if strings.Contains(c, "/var/lib/ghafk") && !strings.HasPrefix(c, "-u ghafk ") {
			t.Errorf("root touches the engine home: %q", c)
		}
	}
	if envContent != "GH_TOKEN=old\n" {
		t.Errorf("migrated env = %q; want only the token, since agents could plant other lines in the old file", envContent)
	}
}

func TestEngineReadsItsConfigFromTheRootOwnedDirectory(t *testing.T) {
	unit := engineServiceUnit(linuxEngine)
	if !strings.Contains(unit, "Environment="+configDirEnv+"=/etc/ghafk\n") {
		t.Errorf("the service does not point the engine at /etc/ghafk:\n%s", unit)
	}
	if strings.Contains(unit, "ReadWritePaths=/etc") {
		t.Error("the service can write its own config")
	}
	if plist := enginePlist(darwinEngine, 2); !strings.Contains(plist, "<key>"+configDirEnv+"</key>\n\t\t<string>/usr/local/etc/ghafk</string>") {
		t.Errorf("the LaunchDaemon does not point the engine at /usr/local/etc/ghafk:\n%s", plist)
	}
}
