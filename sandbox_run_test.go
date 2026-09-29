package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestSandboxRunHidesHomeAndGuardsGit(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "secret-marker"), []byte("person-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := gitClone(t)
	work := workDir(home, repo, "9")
	if err := addWorktree(repo, work, "-b", "agent/9", "origin/main"); err != nil {
		t.Fatal(err)
	}
	defer discardWork(repo, work, "agent/9")
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: work, repo: repo, home: home, timeout: time.Minute,
		role:    &harness.Role{Command: "sh"},
		command: `cat "$HOME/secret-marker" 2>&1; echo "home-listing:$(ls -a $HOME | tr '\n' ' ')"; GD=$(sed 's/^gitdir: //' .git); test -r "$GD/HEAD" && echo git-readable; touch "$GD/HEAD" 2>/dev/null && echo GIT-WRITABLE || echo git-locked; touch made-by-agent.txt; test -w "$TMPDIR" && echo tmp-writable`,
	}, &out, os.Stderr)
	if err != nil {
		t.Fatalf("sandboxed run failed: %v\n%s", err, out.String())
	}
	got := out.String()
	_, listing, _ := strings.Cut(got, "home-listing:")
	listing, _, _ = strings.Cut(listing, "\n")
	if strings.Contains(got, "person-secret") || strings.Contains(listing, "secret-marker") {
		t.Fatalf("the person's home leaked into the sandbox:\n%s", got)
	}
	if !strings.Contains(got, "git-readable") || !strings.Contains(got, "git-locked") {
		t.Fatalf("the repository .git must be readable and locked:\n%s", got)
	}
	if strings.Contains(got, "GIT-WRITABLE") {
		t.Fatalf("the agent wrote into .git:\n%s", got)
	}
	if !strings.Contains(got, "tmp-writable") {
		t.Fatalf("TMPDIR must be writable:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(work, "made-by-agent.txt")); err != nil {
		t.Fatalf("the worktree must accept writes: %v", err)
	}
}

func TestSandboxRunDeletesTheRunHomeAfterwards(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), command: `echo hi > "$HOME/note.txt"; echo done`, timeout: time.Minute, role: &harness.Role{Command: "sh"}, home: home}, &out, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "note.txt")); err == nil {
		t.Fatal("the run home must not be the person's real home")
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".ghafk", "run"))
	if len(entries) != 0 {
		t.Fatalf("run scratch dirs were left behind: %v", entries)
	}
}

func TestSandboxNetworkFlowsThroughTheProxy(t *testing.T) {
	needSandbox(t)
	oldDial := proxyDialFunc
	proxyDialFunc = func(host, port string) (net.Conn, error) {
		return net.DialTimeout("tcp", net.JoinHostPort(host, port), 20*time.Second)
	}
	t.Cleanup(func() { proxyDialFunc = oldDial })
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl is not installed: %v", err)
	}
	stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "EGRESS-OK")
	}))
	defer stub.Close()
	target := strings.TrimPrefix(stub.URL, "https://")
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), egress: []string{"127.0.0.1"}, timeout: time.Minute,
		command: "curl -sk --noproxy \"\" --max-time 20 https://" + target + "/",
		home:    t.TempDir(),
	}, &out, os.Stderr)
	if err != nil {
		t.Fatalf("an allowed host must be reachable through the proxy: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "EGRESS-OK") {
		t.Fatalf("the tunneled body did not arrive: %q", out.String())
	}
	err = runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), timeout: time.Minute,
		command: "curl -sk --noproxy \"\" --max-time 20 https://" + target + "/",
		home:    t.TempDir(),
	}, &out, os.Stderr)
	if err == nil {
		t.Fatal("a host outside the egress list must not be reachable")
	}
	if !strings.Contains(err.Error(), "the sandbox proxy denied: 127.0.0.1") {
		t.Fatalf("the denial must name the host: %v", err)
	}
}

func TestSandboxDirectNetworkIsDead(t *testing.T) {
	needSandbox(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl is not installed: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	hits := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits <- struct{}{}
			_, _ = c.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n"))
			c.Close()
		}
	}()
	url := "http://" + ln.Addr().String() + "/"
	if err := exec.Command("curl", "-s", "--noproxy", "*", "--max-time", "5", url).Run(); err != nil {
		t.Fatalf("the listener must be reachable outside the sandbox: %v", err)
	}
	<-hits
	var out bytes.Buffer
	err = runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), timeout: 30 * time.Second,
		command: "curl -s --noproxy '*' --max-time 5 --connect-timeout 3 " + url + " >/dev/null 2>&1; echo curl-exit:$?",
		home:    t.TempDir(),
	}, &out, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "curl-exit:") || strings.Contains(out.String(), "curl-exit:0") {
		t.Fatalf("direct network access must fail inside the sandbox: %q", out.String())
	}
	select {
	case <-hits:
		t.Fatal("a connection from inside the sandbox reached a listener on the host")
	default:
	}
}

func TestSandboxWorkerCannotRedirectGhafkGit(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	repo := gitClone(t)
	work := workDir(home, repo, "11")
	if err := addWorktree(repo, work, "-b", "agent/11", "origin/main"); err != nil {
		t.Fatal(err)
	}
	defer discardWork(repo, work, "agent/11")
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: work, repo: repo, home: home, timeout: time.Minute,
		role:    &harness.Role{Command: "sh"},
		command: `echo "gitdir: /tmp/evil" > .git`,
	}, &bytes.Buffer{}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "git pointer") {
		t.Fatalf("a tampered worktree pointer must fail the run: %v", err)
	}
}

func TestSandboxPassesStdinToTheHarness(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: home, home: home, timeout: time.Minute,
		role: &harness.Role{Command: "sh"}, command: `cat; echo ":end"`,
		stdin: "PROMPT-BODY",
	}, &out, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "PROMPT-BODY") || !strings.Contains(out.String(), ":end") {
		t.Fatalf("stdin did not reach the harness: %q", out.String())
	}
}

func TestSandboxCopiesHarnessConfigAndNeverWritesItBack(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: "sh", Profile: "codex"}
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute,
		role:    &role,
		command: `grep -q 'model = "x"' "$HOME/.codex/config.toml" && echo config-readable; echo hacked >> "$HOME/.codex/config.toml"`,
	}, &out, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "config-readable") {
		t.Fatalf("the harness config must be visible at its home path inside the sandbox:\n%s", got)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(string(data), "hacked") {
		t.Fatal("the real harness config changed")
	}
}

func TestSandboxLoginCopyBackOnlyWhenChanged(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(login, []byte(`{"tokens":{"refresh_token":"stable"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(login)
	if err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: "sh", Profile: "codex"}
	err = runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute, role: &role, command: "true"}, &bytes.Buffer{}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(login)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an untouched login file must not be rewritten")
	}
	err = runSandboxed(sandboxOpts{
		name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute,
		role:    &role,
		command: `echo '{"tokens":{"refresh_token":"refreshed"}}' > "$HOME/.codex/auth.json"`,
	}, &bytes.Buffer{}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(login)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"refresh_token": "refreshed"`) {
		t.Fatalf("a refreshed login did not copy back: %q", data)
	}
	if info, err := os.Stat(login); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the copied-back login lost its mode: %v %v", info, err)
	}
}

func TestASetupFailureLeavesNoRunOrSocketDirectory(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute,
		role: &harness.Role{Command: "sh", Profile: "codex"}, command: "true",
	}, &bytes.Buffer{}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "codex has no login file") {
		t.Fatalf("the missing login must fail the run: %v", err)
	}
	runs, _ := os.ReadDir(filepath.Join(home, ".ghafk", "run"))
	if len(runs) != 0 {
		t.Fatalf("run scratch directories survived a setup failure: %v", runs)
	}
	sockets, _ := os.ReadDir(xdg)
	if len(sockets) != 0 {
		t.Fatalf("socket directories survived a setup failure: %v", sockets)
	}
}

func TestAFailedRunStillCopiesBackAValidLoginRefresh(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(login, []byte(`{"tokens":{"refresh_token":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: "sh", Profile: "codex"}
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute,
		role: &role, command: `echo '{"tokens":{"refresh_token":"new"}}' > "$HOME/.codex/auth.json"; exit 3`,
	}, &bytes.Buffer{}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("the run must fail: %v", err)
	}
	data, err := os.ReadFile(login)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"refresh_token": "new"`) {
		t.Fatalf("a rotated token from a failed run was dropped: %q", data)
	}
}

func TestAPathLinkIntoAPrivateFolderExposesOnlyTheProgram(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	private := filepath.Join(home, "private-project")
	bin := filepath.Join(home, ".local", "bin")
	for _, dir := range []string{private, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(private, "tool.sh"), []byte("#!/bin/sh\necho tool-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "notes-marker.txt"), []byte("private-notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(private, "tool.sh"), filepath.Join(bin, "review-tool")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), home: home, timeout: time.Minute,
		command: `review-tool; cat "` + filepath.Join(private, "notes-marker.txt") + `" 2>&1; true`,
	}, &out, os.Stderr)
	if err != nil {
		t.Fatalf("sandboxed run failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "tool-ran") {
		t.Fatalf("the linked program did not run:\n%s", out.String())
	}
	if strings.Contains(out.String(), "private-notes") {
		t.Fatalf("the folder the link points into leaked into the sandbox:\n%s", out.String())
	}
}

func TestAnEnvLinePassesItsVariableIntoTheRun(t *testing.T) {
	needSandbox(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("env: NPM_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnv, dir)
	t.Setenv("NPM_TOKEN", "npm-value")
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), home: t.TempDir(), timeout: time.Minute,
		command: `echo "npm:${NPM_TOKEN:-missing}"`,
	}, &out, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "npm:npm-value") {
		t.Fatalf("an env: line must pass its variable into the run: %q", out.String())
	}
}

func TestAMachineEgressLineAllowsAHostAndDenialsAreReportedOnSuccess(t *testing.T) {
	needSandbox(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl is not installed: %v", err)
	}
	run := func() string {
		var stderr bytes.Buffer
		err := runSandboxed(sandboxOpts{
			name: "checks", dir: t.TempDir(), home: t.TempDir(), timeout: time.Minute,
			command: `curl -s --max-time 5 https://models.example.test/ >/dev/null 2>&1; true`,
		}, io.Discard, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		return stderr.String()
	}
	if got := run(); !strings.Contains(got, "denied: models.example.test") {
		t.Fatalf("a denied host must be reported even when the run succeeds: %q", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("egress: models.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnv, dir)
	if got := run(); strings.Contains(got, "denied: models.example.test") {
		t.Fatalf("a machine egress line must allow its host in every run: %q", got)
	}
}

func TestGitRunsInsideTheSandbox(t *testing.T) {
	needSandbox(t)
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), timeout: 60 * time.Second,
		command: "git --version",
		home:    t.TempDir(),
	}, &out, &out)
	if err != nil || !strings.Contains(out.String(), "git version") {
		t.Fatalf("git must run inside the sandbox: %v\n%s", err, out.String())
	}
}

func TestALocalPortIsReachableOnlyWhenTheConfigNamesIt(t *testing.T) {
	needSandbox(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl is not installed: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 8\r\n\r\nlocal-ok"))
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	command := fmt.Sprintf("curl -s --max-time 5 http://localhost:%d/; echo; echo curl-exit:$?", port)
	run := func() string {
		var out bytes.Buffer
		if err := runSandboxed(sandboxOpts{name: "checks", dir: t.TempDir(), timeout: 30 * time.Second, command: command, home: t.TempDir()}, &out, os.Stderr); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := run(); strings.Contains(out, "local-ok") {
		t.Fatalf("a local port must be closed unless the config names it: %q", out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(fmt.Sprintf("local: %d\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnv, dir)
	if out := run(); !strings.Contains(out, "local-ok") {
		t.Fatalf("a local: port must reach the service on this machine: %q", out)
	}
}
