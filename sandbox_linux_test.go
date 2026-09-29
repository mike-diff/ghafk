//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestBwrapArgsOrderAndMounts(t *testing.T) {
	home := t.TempDir()
	script := harnessScript(t, "true\n")
	repo := gitClone(t)
	work := workDir(home, repo, "5")
	if err := addWorktree(repo, work, "-b", "agent/5", "origin/main"); err != nil {
		t.Fatal(err)
	}
	defer discardWork(repo, work, "agent/5")
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: work, command: script, timeout: time.Minute, role: &harness.Role{Command: script}, repo: repo, home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	args := bwrapArgs(spec, 3199, work, "true")
	joined := strings.Join(args, "\n")
	hasPair := func(flag, a, b string) bool {
		for i := 0; i+2 < len(args); i++ {
			if args[i] == flag && args[i+1] == a && args[i+2] == b {
				return true
			}
		}
		return false
	}
	hasFlag := func(v string) bool { return indexOfArg(args, v) >= 0 }
	if !hasPair("--ro-bind", spec.repoGit, spec.repoGit) ||
		!hasPair("--bind", spec.cacheDir, spec.cacheDir) || !hasPair("--bind", spec.runHome, spec.personHome) ||
		!hasPair("--bind", work, work) || !hasPair("--bind", spec.sockDir, spec.sockDir) {
		t.Fatalf("bwrap args miss a mount:\n%s", joined)
	}
	for _, flag := range []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--unshare-net", "--die-with-parent", "--chdir"} {
		if !hasFlag(flag) {
			t.Fatalf("bwrap args miss %s:\n%s", flag, joined)
		}
	}
	for _, banned := range []string{"--clearenv", "--setenv"} {
		if hasFlag(banned) {
			t.Fatalf("bwrap args must not put the environment on the command line:\n%s", joined)
		}
	}
	i := indexOfArg(args, "__sandbox")
	if i < 0 || i+5 >= len(args) || args[i+1] != proxySocketPath(spec.sockDir) || args[i+2] != "3199" || args[i+3] != "--" || args[i+4] != "/bin/sh" || args[i+5] != "-c" {
		t.Fatalf("bwrap args must run the relay init:\n%s", joined)
	}
	hide := -1
	for j := 0; j+1 < len(args); j++ {
		if args[j] == "--tmpfs" && args[j+1] == spec.homeHide {
			hide = j
			break
		}
	}
	ph, rd, wk := indexOfArg(args, spec.runHome), indexOfArg(args, spec.runDir), indexOfArg(args, spec.workDir)
	if !(hide >= 0 && hide < ph && ph < rd && ph < wk) {
		t.Fatalf("the home tmpfs must precede the fresh-home bind, and the run scratch must be rebound after it:\n%s", joined)
	}
}

func indexOfArg(args []string, value string) int {
	for i, a := range args {
		if a == value {
			return i
		}
	}
	return -1
}

func TestSandboxPreflightRejectsAHarnessThatCannotStart(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	broken := filepath.Join(t.TempDir(), "broken-harness")
	if err := os.WriteFile(broken, []byte("#!/definitely/not/a/real/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runSandboxed(sandboxOpts{
		name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute,
		role:    &harness.Role{Command: broken + " --flag"},
		command: "echo should-not-run",
	}, &bytes.Buffer{}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "does not start inside the sandbox") {
		t.Fatalf("a harness that cannot load must fail the preflight: %v", err)
	}
}

func TestAnInstallerShimRunsInsideTheSandboxWithARealHome(t *testing.T) {
	needSandbox(t)
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		t.Skipf("pnpm is not installed: %v", err)
	}
	if shimTarget(evalPath(pnpm)) == "" {
		t.Skipf("this pnpm has no cmd-shim-target marker: %s", pnpm)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(home) != "/home" && filepath.Dir(home) != "/Users" {
		t.Skipf("home %s does not sit under /home", home)
	}
	var out bytes.Buffer
	role := harness.Role{Command: "pnpm"}
	err = runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, timeout: 2 * time.Minute, role: &role, command: "pnpm --version"}, &out, os.Stderr)
	if err != nil {
		t.Fatalf("the installer-shim harness failed inside the sandbox: %v\n%s", err, out.String())
	}
	if !regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(out.String()) {
		t.Fatalf("pnpm did not print a version: %q", out.String())
	}
}

func TestAPnpmInstalledCodexRunsInsideTheSandbox(t *testing.T) {
	needSandbox(t)
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex is not installed: %v", err)
	}
	if shimTarget(evalPath(codex)) == "" {
		t.Skipf("this codex has no cmd-shim-target marker: %s", codex)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(home) != "/home" && filepath.Dir(home) != "/Users" {
		t.Skipf("home %s does not sit under /home", home)
	}
	var out, errb bytes.Buffer
	role := harness.Role{Command: "codex"}
	err = runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, timeout: 2 * time.Minute, role: &role, command: "codex --version"}, &out, &errb)
	if err != nil {
		t.Fatalf("pnpm-installed codex failed inside the sandbox: %v\n%s\n%s", err, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "codex-cli") {
		t.Fatalf("codex did not print its version: %q", out.String())
	}
}

func TestTheSandboxRunsInItsOwnSessionWithoutNestedNamespaces(t *testing.T) {
	needSandbox(t)
	var out bytes.Buffer
	err := runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), home: t.TempDir(), timeout: time.Minute,
		command: `echo "session=$(cut -d' ' -f6 /proc/self/stat)"; if ! command -v unshare >/dev/null; then echo userns=untestable; elif unshare -U true 2>/dev/null; then echo userns=allowed; else echo userns=blocked; fi`,
	}, &out, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "session=0") || !strings.Contains(out.String(), "session=") {
		t.Fatalf("the run shares ghafk's session, so it keeps ghafk's terminal: %s", out.String())
	}
	if strings.Contains(out.String(), "userns=untestable") {
		t.Skip("unshare is not installed")
	}
	if !strings.Contains(out.String(), "userns=blocked") {
		t.Fatalf("the run can create nested user namespaces: %s", out.String())
	}
}

func TestAHomeReachedThroughASymlinkStaysHidden(t *testing.T) {
	needSandbox(t)
	real, err := os.MkdirTemp("/var/tmp", "ghafk-home-")
	if err != nil {
		t.Skipf("no writable directory outside the hidden trees: %v", err)
	}
	defer os.RemoveAll(real)
	marker := filepath.Join(real, "secret-marker")
	if err := os.WriteFile(marker, []byte("person-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, home); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runSandboxed(sandboxOpts{
		name: "checks", dir: t.TempDir(), home: home, timeout: time.Minute,
		command: "cat " + marker + " 2>&1 || echo hidden",
	}, &out, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "person-secret") {
		t.Fatalf("the real home behind a symlinked home is readable inside the sandbox: %s", out.String())
	}
}

func TestTheGhafkBinaryIsBoundAloneNotItsDirectory(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "build", "ghafk")
	spec := sandboxSpec{personHome: home, homeHide: home, runHome: filepath.Join(home, ".ghafk", "run", "x", "home"), runDir: filepath.Join(home, ".ghafk", "run", "x"), cacheDir: t.TempDir(), sockDir: t.TempDir(), ghafkBin: bin}
	args := strings.Join(bwrapArgs(spec, 1, t.TempDir(), "true"), "\n")
	if !strings.Contains(args, "--ro-bind\n"+bin+"\n"+bin) {
		t.Fatalf("the ghafk binary under the home must be bound read-only:\n%s", args)
	}
	if strings.Contains(args, "\n"+filepath.Dir(bin)+"\n") {
		t.Fatalf("the directory of the ghafk binary must not be bound:\n%s", args)
	}
}

func TestArgvHoldsNoSecrets(t *testing.T) {
	needSandbox(t)
	secret := "sk-argv-secret-marker-value"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	home := t.TempDir()
	role := harness.Role{Command: "sh", Profile: "claude"}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "sh", timeout: time.Minute, role: &role, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(sandboxEnv(spec, spec.personHome, "http://127.0.0.1:1"), "\n")
	if !strings.Contains(env, "ANTHROPIC_API_KEY="+secret) {
		t.Fatalf("the key must reach the launcher's environment:\n%s", env)
	}
	for _, arg := range bwrapArgs(spec, 1, t.TempDir(), "echo key=$ANTHROPIC_API_KEY") {
		if strings.Contains(arg, secret) {
			t.Fatalf("the secret is in the bwrap command line: %q", arg)
		}
	}
	var out bytes.Buffer
	if err := runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, timeout: time.Minute, role: &role, command: "echo key=$ANTHROPIC_API_KEY"}, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "key="+secret) {
		t.Fatalf("the key must still reach the harness inside the sandbox:\n%s", out.String())
	}
}
