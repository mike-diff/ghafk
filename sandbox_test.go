package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func needSandbox(t *testing.T) {
	t.Helper()
	if err := sandboxReady(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI must provide the OS sandbox, or the sandbox tests prove nothing: %v", err)
		}
		t.Skipf("no OS sandbox on this machine: %v", err)
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__sandbox" {
		if err := sandboxInitCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "ghafk:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	config, err := os.MkdirTemp("", "ghafk-test-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv(configDirEnv, config)
	code := m.Run()
	os.RemoveAll(config)
	os.Exit(code)
}

func TestSandboxCommandTunesHarnessSandboxes(t *testing.T) {
	codex := harness.Builtins()["codex"].Command
	got := sandboxCommand(codex, "codex")
	if !strings.Contains(got, "--sandbox danger-full-access") || strings.Contains(got, "workspace-write") {
		t.Fatalf("codex keeps its own sandbox inside ghafk's: %q", got)
	}
	for _, custom := range []string{"codex exec -s workspace-write -", "codex exec --sandbox=read-only -", "codex exec --full-auto -"} {
		if got := sandboxCommand(custom, "codex"); !strings.Contains(got, "--sandbox danger-full-access") || strings.Contains(got, "workspace-write") || strings.Contains(got, "read-only") || strings.Contains(got, "--full-auto") {
			t.Fatalf("a custom codex line keeps its own sandbox: %q", got)
		}
	}
	if got == codex {
		t.Fatal("codex command was not rewritten")
	}
	claude := harness.Builtins()["claude"].Command
	got = sandboxCommand(claude, "claude")
	if !strings.Contains(got, `--settings '{"sandbox":{"enabled":false}}'`) {
		t.Fatalf("claude keeps its own sandbox inside ghafk's: %q", got)
	}
	if again := sandboxCommand(got, "claude"); again != got {
		t.Fatalf("claude settings appended twice: %q", again)
	}
	if raw := sandboxCommand("my-agent --flag", ""); raw != "my-agent --flag" {
		t.Fatalf("a custom harness command must not change: %q", raw)
	}
}

func TestEgressListScopesModelHostsPerHarness(t *testing.T) {
	allowed := egressList("claude", []string{"npm.internal.example.com"})
	if !hostAllowed("api.anthropic.com", allowed) || !hostAllowed("registry.npmjs.org", allowed) || !hostAllowed("npm.internal.example.com", allowed) {
		t.Fatalf("claude egress lost a needed host: %v", allowed)
	}
	for _, host := range []string{"api.openai.com", "chatgpt.com", "openrouter.ai", "sentry.io", "statsig.anthropic.com", "models.dev"} {
		if hostAllowed(host, allowed) {
			t.Fatalf("claude egress must not include %q: %v", host, allowed)
		}
	}
	opencode := egressList("opencode", nil)
	if !hostAllowed("models.opencode.ai", opencode) || !hostAllowed("models.dev", opencode) {
		t.Fatalf("opencode egress lost its model catalog hosts: %v", opencode)
	}
	codex := egressList("codex", nil)
	if !hostAllowed("api.openai.com", codex) || !hostAllowed("chatgpt.com", codex) || hostAllowed("api.anthropic.com", codex) {
		t.Fatalf("codex egress is not scoped: %v", codex)
	}
	checks := egressList("", nil)
	if !hostAllowed("proxy.golang.org", checks) || !hostAllowed("pypi.org", checks) || hostAllowed("api.anthropic.com", checks) || hostAllowed("sentry.io", checks) {
		t.Fatalf("a checks run must reach registries and no model or telemetry host: %v", checks)
	}
	if count := strings.Count(strings.Join(egressList("claude", []string{"API.ANTHROPIC.COM"}), ","), "api.anthropic.com"); count != 1 {
		t.Fatalf("api.anthropic.com appears %d times", count)
	}
}

func TestCommandWordsSkipsQuotedTextAndAssignments(t *testing.T) {
	words := commandWords("go build ./... && test -z \"$(gofmt -l .)\" || pnpm -r test; echo done")
	want := []string{"go", "gofmt", "pnpm"}
	if strings.Join(words, ",") != strings.Join(want, ",") {
		t.Fatalf("words = %v, want %v", words, want)
	}
	if words := commandWords("FOO=1 ./run.sh x | tee out.txt"); strings.Join(words, ",") != "tee" {
		t.Fatalf("assignments and paths must not count as programs: %v", words)
	}
	if words := commandWords(`echo "(definitely-not-a-program here)" 2>&1 | sort`); strings.Join(words, ",") != "sort" {
		t.Fatalf("quoted text inside parentheses must not count as a program: %v", words)
	}
	if words := commandWords("true 2>&1; :; echo x"); strings.Join(words, ",") != "" {
		t.Fatalf("builtins and redirection tails must not count: %v", words)
	}
}

func TestSandboxEnvPassesOnlyAllowedKeys(t *testing.T) {
	t.Setenv("GH_TOKEN", "github-secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("OPENAI_BASE_URL", "https://stub.internal")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-test")
	t.Setenv("NPM_TOKEN", "npm-secret")
	t.Setenv("HARMLESS", "x")
	spec := sandboxSpec{personHome: "/home/u", runHome: "/home/u/.ghafk/run/1/home", cacheDir: "/home/u/.ghafk/cache/repo-1", path: "/usr/bin", profile: "claude"}
	env := sandboxEnv(spec, "/home/u", "http://127.0.0.1:3128")
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"HOME=/home/u",
		"TMPDIR=/home/u/tmp",
		"CLAUDE_CODE_TMPDIR=/home/u/tmp",
		"HTTPS_PROXY=http://127.0.0.1:3128",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"GOCACHE=/home/u/.ghafk/cache/repo-1/go-build",
		"GOMODCACHE=/home/u/.ghafk/cache/repo-1/go-mod",
		"pnpm_config_store_dir=/home/u/.ghafk/cache/repo-1/pnpm",
		"pnpm_config_cache_dir=/home/u/.ghafk/cache/repo-1/pnpm-cache",
		"GOPATH=/home/u/go",
		"CARGO_HOME=/home/u/.cargo",
		"PIP_CACHE_DIR=/home/u/.cache/pip",
		"ANTHROPIC_API_KEY=sk-test",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-test",
	} {
		if !strings.Contains("\n"+joined+"\n", "\n"+want+"\n") {
			t.Fatalf("env is missing %q:\n%s", want, joined)
		}
	}
	for _, leak := range []string{"github-secret", "npm-secret", "HARMLESS=", "OPENAI_BASE_URL"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("env leaked %q:\n%s", leak, joined)
		}
	}
	openai := sandboxEnv(sandboxSpec{runHome: "/r/home", cacheDir: "/r/cache", path: "/usr/bin", profile: "codex"}, "/r/home", "http://127.0.0.1:1")
	if !strings.Contains(strings.Join(openai, "\n"), "OPENAI_BASE_URL=") || strings.Contains(strings.Join(openai, "\n"), "ANTHROPIC_API_KEY=") {
		t.Fatalf("codex env is not scoped to its provider:\n%s", strings.Join(openai, "\n"))
	}
}

func TestSandboxEnvPassesOnlyListedNames(t *testing.T) {
	t.Setenv("NPM_TOKEN", "npm-secret")
	spec := sandboxSpec{personHome: "/home/u", runHome: "/r/home", cacheDir: "/r/cache", path: "/bin", profile: ""}
	env := sandboxEnv(spec, "/r/home", "http://127.0.0.1:1")
	if strings.Contains(strings.Join(env, "\n"), "NPM_TOKEN=") {
		t.Fatal("NPM_TOKEN must not pass without an env: line")
	}
	spec.passEnv = []string{"NPM_TOKEN", "SSL_CERT_FILE"}
	env = sandboxEnv(spec, "/r/home", "http://127.0.0.1:1")
	if !strings.Contains(strings.Join(env, "\n"), "NPM_TOKEN=npm-secret") {
		t.Fatalf("an env: line must pass NPM_TOKEN:\n%s", strings.Join(env, "\n"))
	}
}

func TestSandboxEnvKey(t *testing.T) {
	yes := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY", "TERM", "LANG", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE"}
	no := []string{"GH_TOKEN", "GITHUB_TOKEN", "NPM_TOKEN", "CLOUDFLARE_API_TOKEN", "CLAUDE_CODE_MESSAGING_TOKEN", "PATH", "HOME"}
	for _, key := range yes {
		if !sandboxEnvKey(key, "pi", nil) {
			t.Fatalf("%s must pass for the pi family", key)
		}
	}
	for _, key := range no {
		if sandboxEnvKey(key, "pi", nil) {
			t.Fatalf("%s must not pass", key)
		}
	}
	if !sandboxEnvKey("NPM_TOKEN", "", []string{"NPM_TOKEN"}) {
		t.Fatal("an env: config line must pass its keys")
	}
	if sandboxEnvKey("OPENAI_API_KEY", "claude", nil) {
		t.Fatal("claude must not carry another provider's key")
	}
}

func TestSandboxLoginFilesKnowEachHarness(t *testing.T) {
	for _, k := range append([]string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_API_KEY"}, sandboxModelEnvKeys...) {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	for _, rel := range []string{".codex", ".omp/agent", ".sesh"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{".codex/auth.json", ".omp/agent/auth.json", ".sesh/credentials.json", ".sesh/key"} {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(rel)), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for profile, want := range map[string][]string{
		"codex": {".codex/auth.json"},
		"sesh":  {".sesh/credentials.json", ".sesh/key"},
	} {
		logins, err := sandboxLoginFiles(profile, home)
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		var got []string
		for _, login := range logins {
			rel, _ := filepath.Rel(home, login)
			got = append(got, filepath.ToSlash(rel))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s logins = %v, want %v", profile, got, want)
		}
	}
	if _, err := sandboxLoginFiles("claude", home); err == nil {
		t.Fatal("claude without a token or credentials file must fail the preflight")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	if logins, err := sandboxLoginFiles("claude", home); err != nil || logins != nil {
		t.Fatalf("claude with an env token must pass without binds: %v, %v", logins, err)
	}
	if _, err := sandboxLoginFiles("unknown-harness", home); err != nil {
		t.Fatalf("a harness without a login table must pass: %v", err)
	}
	if _, err := sandboxLoginFiles("opencode", home); err == nil || !strings.Contains(err.Error(), "opencode has no login file") {
		t.Fatalf("opencode without a login must explain the fix: %v", err)
	}
	if _, err := sandboxLoginFiles("omp", home); err == nil || !strings.Contains(err.Error(), "ignores the egress proxy") {
		t.Fatalf("omp must be refused with the reason: %v", err)
	}
}

func TestBuildSandboxParksWhenTheHarnessProgramIsMissing(t *testing.T) {
	home := t.TempDir()
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: home, command: "definitely-not-a-program-xyz --flag", timeout: time.Minute, role: &harness.Role{Command: "definitely-not-a-program-xyz --flag", Profile: ""}, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err == nil || !strings.Contains(err.Error(), "not on PATH") || !strings.Contains(err.Error(), "bind:") {
		t.Fatalf("missing harness program must park with the fix: %v", err)
	}
}

func TestBuildSandboxResolvesProgramsAndRepoGit(t *testing.T) {
	home := t.TempDir()
	script := harnessScript(t, "true\n")
	repo := gitClone(t)
	work := runWork(home, repo, "5")
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
	if !slices.Contains(spec.programs, script) {
		t.Fatalf("the harness program %s was not resolved: %v", script, spec.programs)
	}
	if spec.repoGit != evalPath(filepath.Join(repo, ".git")) {
		t.Fatalf("repo git dir = %q, want %q", spec.repoGit, evalPath(filepath.Join(repo, ".git")))
	}
	if want := filepath.Join(home, ".ghafk", "cache", repoSlug(repo)); runtime.GOOS == "linux" && spec.cacheDir != want {
		t.Fatalf("cache dir = %q, want the shared per-repo cache %q", spec.cacheDir, want)
	}
	if runtime.GOOS == "darwin" && !strings.HasPrefix(spec.cacheDir, spec.runDir+string(os.PathSeparator)) {
		t.Fatalf("cache dir = %q, want a per-run cache under %q on macOS", spec.cacheDir, spec.runDir)
	}
	if spec.personHome != home {
		t.Fatalf("person home = %q", spec.personHome)
	}
	for _, sub := range sharedCacheDirs() {
		if _, err := os.Stat(filepath.Join(spec.cacheDir, sub)); err != nil {
			t.Fatalf("cache %s missing: %v", sub, err)
		}
	}
	if len(spec.logins) != 0 {
		t.Fatalf("a profile without a login table must bind none: %v", spec.logins)
	}
}

func TestProgramBindDirsResolveSymlinksAndPackageRoots(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "pkg", "node_modules", "@scope", "tool", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(base, "pkg", "node_modules", "@scope", "tool", "bin", "cli.js")
	if err := os.WriteFile(cli, []byte("#!/usr/bin/env node\nconsole.log(1)\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "shims"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(base, "shims", "tool")
	if err := os.Symlink(cli, shim); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	dirs := programBindDirs(shim)
	has := func(p string) bool { return slices.Contains(dirs, p) || slices.Contains(dirs, evalPath(p)) }
	if !has(filepath.Join(base, "shims")) {
		t.Fatalf("the shim directory must be bound: %v", dirs)
	}
	if !has(filepath.Join(base, "pkg", "node_modules", "@scope", "tool", "bin")) {
		t.Fatalf("the resolved script directory must be bound: %v", dirs)
	}
	if !has(filepath.Join(base, "pkg", "node_modules", "@scope", "tool")) {
		t.Fatalf("the package directory must be bound: %v", dirs)
	}
	if has(filepath.Join(base, "pkg")) || has(base) {
		t.Fatalf("the whole project must not be bound: %v", dirs)
	}
}

func TestNodePackageDirHandlesScopesAndDotBin(t *testing.T) {
	root := filepath.Join("proj", "node_modules")
	cases := map[string]string{
		filepath.Join(root, "typescript", "bin", "tsc"):  filepath.Join(root, "typescript"),
		filepath.Join(root, "@scope", "pkg", "bin", "x"): filepath.Join(root, "@scope", "pkg"),
	}
	for path, want := range cases {
		if got := nodePackageDir(path); got != want {
			t.Fatalf("nodePackageDir(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestProgramBindDirsBindTheGoToolchainRoot(t *testing.T) {
	bin := filepath.Join("home", "u", "go", "pkg", "mod", "golang.org", "toolchain@v0.0.1", "bin", "go")
	dirs := programBindDirs(bin)
	joined := strings.Join(dirs, "\n")
	want := filepath.Join("home", "u", "go", "pkg", "mod", "golang.org", "toolchain@v0.0.1")
	if !strings.Contains(joined, want) {
		t.Fatalf("the toolchain root must be bound: %v", dirs)
	}
}

func TestRemoveTreeClearsReadOnlyTrees(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "go-mod", "cache", "download", "example.com", "@v")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "list"), []byte("v1"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, ro := range []string{deep, filepath.Dir(deep), filepath.Dir(filepath.Dir(deep)), filepath.Dir(filepath.Dir(filepath.Dir(deep)))} {
		if err := os.Chmod(ro, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	removeTree(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the read-only cache tree survived: %v", err)
	}
}

func TestTheProxySocketPathStaysShort(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 90))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", long)
	t.Setenv("TMPDIR", long)
	sockDir, err := newSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(sockDir)
	if path := proxySocketPath(sockDir); len(path) > 100 {
		t.Fatalf("socket path %q is %d bytes, over the unix socket limit", path, len(path))
	}
}

func TestSeatbeltProfileAllowsOnlyTheRun(t *testing.T) {
	profile := seatbeltProfile(seatbeltSpec{
		SystemReads: []string{"/usr", "/bin"},
		Reads:       []string{"/Users/u/.local/share/pnpm/global"},
		Writes:      []string{"/Users/u/src/work/app/r-abcd", "/Users/u/.ghafk/run/1"},
		Execs:       []string{"/usr", "/Users/u/.local/share/pnpm/global"},
		Home:        "/Users/u",
	}, "3199")
	for _, want := range []string{
		"(deny default)",
		`(allow file-read* (literal "/"))`,
		`(path-ancestors "/Users/u/src/work/app/r-abcd")`,
		`(path-ancestors "/Users/u/.ghafk/run/1")`,
		`(path-ancestors "/Users/u/.local/share/pnpm/global")`,
		`(literal "/tmp")`,
		`(literal "/dev/null")`,
		`(allow file-read-data (literal "/Users/u/.CFUserTextEncoding"))`,
		`(allow user-preference-read (preference-domain "kCFPreferencesAnyApplication"))`,
		`com.apple.trustd.agent`,
		`(sysctl-name "machdep.cpu.brand_string")`,
		`(subpath "/Users/u/src/work/app/r-abcd")`,
		`(allow process-exec (subpath "/usr") (subpath "/Users/u/.local/share/pnpm/global") (subpath "/Users/u/src/work/app/r-abcd") (subpath "/Users/u/.ghafk/run/1")`,
		`(allow network-outbound (remote tcp "localhost:3199"))`,
		"(allow process-fork)",
		"(allow signal (target same-sandbox))",
		"(allow process-info* (target same-sandbox))",
		`(sysctl-name-prefix "hw.")`,
		`(sysctl-name "kern.bootargs")`,
		`(sysctl-name "security.mac.lockdown_mode_state")`,
		"com.apple.cfprefsd.daemon",
		"com.apple.system.opendirectoryd.libinfo",
		"com.apple.logd",
		`(allow file-map-executable)`,
		`(literal "/dev/fd")`,
		`(literal "/dev/tty")`,
		`(literal "/dev/ptmx")`,
		`(allow ipc-posix-shm-read* (ipc-posix-name "apple.shm.notification_center") (ipc-posix-name-prefix "apple.cfprefs."))`,
		`/dev/autofs_nowait`,
		"/private/tmp/pnpm-store-operation-locks-",
		"(deny mach-lookup (global-name \"com.apple.securityd\") (global-name \"com.apple.SecurityServer\"))",
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	for _, banned := range []string{
		"com.apple.lsd.core",
		"com.apple.lsd.identity",
		"com.apple.lsd.openentry",
		"com.apple.CoreServices.coreservicesd",
		"com.apple.fonts",
		"com.apple.FontObjectsServer",
		"kern.proc.all",
		`(subpath "/opt")`,
		`/private/var/tmp`,
		"ipc-posix-shm-name",
		"(allow ipc-posix-shm-read*)\n",
		"(allow ipc-posix-shm-write-data)\n",
		`(allow file-read* (subpath "/Users/u/src/work/app"))`,
		`(allow file-read* (subpath "/Users/u/src/work"))`,
		`(allow file-read* (subpath "/Users/u/src"))`,
		`(allow file-read* (subpath "/Users/u"))`,
		`(allow file-read* (subpath "/Users"))`,
		"(allow sysctl-read)\n",
	} {
		if strings.Contains(profile, banned) {
			t.Fatalf("profile must not grant reads of the home through %q:\n%s", banned, profile)
		}
	}
	if strings.Count(profile, "localhost:3199") != 1 {
		t.Fatalf("the proxy port must appear exactly once:\n%s", profile)
	}
}

func TestUsernsFixPastesFlushLeft(t *testing.T) {
	if sandboxUsernsFix == "" {
		t.Skip("no userns restriction fix on this platform")
	}
	if !strings.Contains(sandboxUsernsFix, "\nuserns,\n") {
		t.Fatalf("the AppArmor rule must be `userns,`:\n%s", sandboxUsernsFix)
	}
	lines := strings.Split(sandboxUsernsFix, "\n")
	inside := false
	for _, line := range lines {
		if strings.HasSuffix(line, "<<'PROFILE'") {
			inside = true
			continue
		}
		if line == "PROFILE" {
			inside = false
			continue
		}
		if inside && line != strings.TrimLeft(line, " \t") {
			t.Fatalf("the AppArmor profile must be flush-left so the heredoc pastes: %q", line)
		}
	}
	if !strings.Contains(sandboxUsernsFix, "sudo tee /etc/apparmor.d/bubblewrap") || !strings.Contains(sandboxUsernsFix, "apparmor_parser") {
		t.Fatalf("the fix must install and load the profile:\n%s", sandboxUsernsFix)
	}
	if !strings.Contains(sandboxUsernsFix, "unconfined") {
		t.Fatalf("the fix must say what the profile does:\n%s", sandboxUsernsFix)
	}
}

func TestEgressFrontMatterAddsHosts(t *testing.T) {
	wf, err := parseWorkflow("---\nworker: mycmd\negress: npm.internal.example.com Npm.Other.Example.COM *.pkg.corp.example\n---\nbody", harness.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(wf.egress, ",") != "npm.internal.example.com,npm.other.example.com,*.pkg.corp.example" {
		t.Fatalf("egress = %v", wf.egress)
	}
	for _, bad := range []string{"https://evil.example/path", "localhost", "10.0.0.1", "2001:db8::1", "*.com", "*.*.example.com", "evil example.com"} {
		if _, err := parseWorkflow("---\nworker: x\negress: "+bad+"\n---\nb", harness.Env{}); err == nil {
			t.Fatalf("egress %q must be rejected", bad)
		}
	}
}

func TestBindAndEnvConfigLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("bind: /opt/tools /custom/bin\nenv: NPM_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings(path)
	if err != nil || strings.Join(s.bind, ",") != "/opt/tools,/custom/bin" || strings.Join(s.env, ",") != "NPM_TOKEN" {
		t.Fatalf("bind = %v, env = %v, %v", s.bind, s.env, err)
	}
	for _, line := range []string{"bind: relative/path\n", "env: 3BAD\n", "env: A-B\n", "env: HOME\n", "env: HTTPS_PROXY\n", "env: GH_TOKEN\n", "env: GHAFK_RUN_ID\n", "env: GOCACHE\n", "env: GOPATH\n", "env: CLAUDE_CODE_TMPDIR\n", "env: npm_config_cache\n", "env: pnpm_config_store_dir\n", "env: DISABLE_TELEMETRY\n"} {
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSettings(path); err == nil {
			t.Fatalf("%q must be rejected", line)
		}
	}
}

func TestWorktreeIntactDetectsPointerTampering(t *testing.T) {
	repo := gitClone(t)
	work := runWork(t.TempDir(), repo, "7")
	if err := addWorktree(repo, work, "-b", "agent/7", "origin/main"); err != nil {
		t.Fatal(err)
	}
	defer discardWork(repo, work, "agent/7")
	if err := worktreeIntact(work); err != nil {
		t.Fatalf("a fresh worktree must be intact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: /tmp/evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := worktreeIntact(work); err == nil || !strings.Contains(err.Error(), "git pointer") {
		t.Fatalf("a moved pointer must fail: %v", err)
	}
	worktreeRemove(repo, work)
	if err := worktreeIntact(work); err != nil {
		t.Fatalf("a removed worktree must not be checked: %v", err)
	}
}

func TestDecorateRunErrorNamesDeniedHosts(t *testing.T) {
	if err := decorateRunError(nil, &egressProxy{}); err != nil {
		t.Fatal(err)
	}
	base := os.ErrClosed
	p := &egressProxy{}
	p.mu.Lock()
	p.denied = []string{"b.example", "a.example"}
	p.mu.Unlock()
	err := decorateRunError(base, p)
	if !strings.Contains(err.Error(), "the sandbox proxy denied: a.example, b.example") {
		t.Fatalf("denied hosts missing from the run error: %v", err)
	}
	if err := decorateRunError(os.ErrClosed, &egressProxy{}); err == nil || strings.Contains(err.Error(), "denied") {
		t.Fatalf("no denials must not add text: %v", err)
	}
}

func TestProxyCloseEndsOpenConnections(t *testing.T) {
	p := &egressProxy{allowed: egressList("", nil)}
	if err := p.listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", p.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		p.mu.Lock()
		tracked := len(p.conns)
		p.mu.Unlock()
		if tracked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the proxy never tracked the open connection")
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("closing the proxy must end its open connections: %v", err)
	}
}

func TestLoginSyncBackRefusesTamperedSeeds(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(real, []byte(`{"tokens":{"refresh_token":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(home, "secret-target")
	if err := os.WriteFile(secret, []byte(`{"tokens":{"refresh_token":"STOLEN"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := sandboxSpec{}
	spec.runHome = t.TempDir()
	if err := os.MkdirAll(filepath.Join(spec.runHome, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	runLogin := func(name string) string {
		return filepath.Join(spec.runHome, ".codex", name)
	}

	spec.logins = []loginSeed{{real: real, run: runLogin("link.json"), runResolved: runLogin("link.json"), orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	if err := os.Symlink(secret, spec.logins[0].run); err != nil {
		t.Fatal(err)
	}
	syncBack(t, spec)
	data, _ := os.ReadFile(real)
	if strings.Contains(string(data), "STOLEN") {
		t.Fatal("a symlinked login seed copied attacker content back")
	}

	oversize := runLogin("big.json")
	spec.logins = []loginSeed{{real: real, run: oversize, runResolved: oversize, orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	if err := os.WriteFile(oversize, []byte(`{"tokens":{"refresh_token":"BIG"},"pad":"`+strings.Repeat("a", loginSizeLimit)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	syncBack(t, spec)
	data, _ = os.ReadFile(real)
	if !strings.Contains(string(data), `"old"`) {
		t.Fatal("an oversized login seed copied back")
	}

	broken := runLogin("broken.json")
	spec.logins = []loginSeed{{real: real, run: broken, runResolved: broken, orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	if err := os.WriteFile(broken, []byte(`not json at all`), 0o600); err != nil {
		t.Fatal(err)
	}
	syncBack(t, spec)
	data, _ = os.ReadFile(real)
	if !strings.Contains(string(data), `"old"`) {
		t.Fatal("an invalid JSON login seed copied back")
	}

	piHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(piHome, "auth.json"), []byte(`{"tokens":{"refresh_token":"PI-SECRET"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedDir := filepath.Join(spec.runHome, ".pi-agent")
	if err := os.Symlink(piHome, linkedDir); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(linkedDir, "auth.json")
	spec.logins = []loginSeed{{real: real, run: linked, runResolved: linked, orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	syncBack(t, spec)
	data, _ = os.ReadFile(real)
	if strings.Contains(string(data), "PI-SECRET") {
		t.Fatal("a run login under a symlinked directory copied another harness's credentials back")
	}

	hard := runLogin("hard.json")
	if err := os.WriteFile(hard, []byte(`{"tokens":{"refresh_token":"hardlinked"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(hard, filepath.Join(spec.runHome, "second-name")); err != nil {
		t.Fatal(err)
	}
	spec.logins = []loginSeed{{real: real, run: hard, runResolved: hard, orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	syncBack(t, spec)
	data, _ = os.ReadFile(real)
	if strings.Contains(string(data), "hardlinked") {
		t.Fatal("a hardlinked login seed copied back")
	}

	good := runLogin("good.json")
	if err := os.WriteFile(good, []byte(`{"tokens":{"refresh_token":"new"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.logins = []loginSeed{{real: real, run: good, runResolved: evalPath(good), orig: []byte(`{"tokens":{"refresh_token":"old"}}`)}}
	syncBack(t, spec)
	data, _ = os.ReadFile(real)
	if !strings.Contains(string(data), `"refresh_token": "new"`) {
		t.Fatal("a legitimate refresh did not copy back")
	}
}

func TestMergeLoginRefreshTakesOnlyTokenFields(t *testing.T) {
	orig := []byte(`{"anthropic":{"type":"oauth","refresh":"r1","access":"a1","expires":100},"openai":{"type":"api_key","key":"sk-1"}}`)
	for _, c := range []struct {
		name, changed, keep, drop string
	}{
		{"a refresh rotates the token fields", `{"anthropic":{"type":"oauth","refresh":"r2","access":"a2","expires":200},"openai":{"type":"api_key","key":"sk-1"}}`, `"refresh":"r2"`, `"r1"`},
		{"a command-valued key is never taken", `{"anthropic":{"type":"oauth","refresh":"r1","access":"a1","expires":100},"openai":{"type":"api_key","key":"!touch marker"}}`, `"key":"sk-1"`, `marker`},
		{"a command-valued token is never taken", `{"anthropic":{"type":"oauth","refresh":"!touch marker","access":"a1","expires":100},"openai":{"type":"api_key","key":"sk-1"}}`, `"refresh":"r1"`, `marker`},
		{"an env-expanded token is never taken", `{"anthropic":{"type":"oauth","refresh":"${HOME}","access":"a1","expires":100},"openai":{"type":"api_key","key":"sk-1"}}`, `"refresh":"r1"`, `HOME`},
		{"a new provider is never added", `{"anthropic":{"type":"oauth","refresh":"r1","access":"a1","expires":100},"openai":{"type":"api_key","key":"sk-1"},"evil":{"type":"api_key","key":"!touch marker"}}`, `"sk-1"`, `evil`},
		{"a credential type is never switched", `{"anthropic":{"type":"api_key","key":"!touch marker","refresh":"r1","access":"a1","expires":100},"openai":{"type":"api_key","key":"sk-1"}}`, `"type":"oauth"`, `marker`},
	} {
		merged, ok := mergeLoginRefresh(orig, []byte(c.changed))
		if !ok {
			t.Fatalf("%s: the merge failed", c.name)
		}
		compact := strings.Join(strings.Fields(string(merged)), "")
		if !strings.Contains(compact, c.keep) || strings.Contains(compact, c.drop) {
			t.Errorf("%s: got %s", c.name, merged)
		}
	}
}

func TestRefusedIPBlocksPrivateAndLoopbackTargets(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.1.2.3", "192.168.1.1", "172.16.0.9", "169.254.1.1", "fe80::1", "224.0.0.1", "0.0.0.0", "fd00::1", "255.255.255.255", "100.64.1.1", "100.127.255.255", "0.1.2.3", "fec0::1", "64:ff9b::1.2.3.4", "2002:dead::1"} {
		if ip := net.ParseIP(s); !refusedIP(ip) {
			t.Fatalf("%s must be refused", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "140.82.121.4", "2607:f8b0:4004:800::200e"} {
		if ip := net.ParseIP(s); refusedIP(ip) {
			t.Fatalf("%s must be allowed", s)
		}
	}
}

func TestSeatbeltPathsCoverTheRunnerGaps(t *testing.T) {
	s := seatbeltPaths(sandboxSpec{workDir: "/Users/u/w", runHome: "/Users/u/r", runDir: "/Users/u/d", cacheDir: "/Users/u/c"})
	joined := strings.Join(append(append([]string{}, s.SystemReads...), s.Reads...), "\n")
	for _, want := range []string{"/private/var/select", "/private/var/db/timezone"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("seatbelt paths missing %q: %s", want, joined)
		}
	}
}

func TestPnpmStoreRootExposesSiblingDependencies(t *testing.T) {
	store := filepath.Join("global", "v11", "hash", "node_modules")
	pkg := filepath.Join(store, ".pnpm", "codex@1.0.0", "node_modules", "codex", "bin", "codex.js")
	if root := pnpmStoreRoot(pkg); root != store {
		t.Fatalf("pnpmStoreRoot = %q, want %q", root, store)
	}
	if root := pnpmStoreRoot(filepath.Join("plain", "node_modules", "pkg", "bin", "x")); root != "" {
		t.Fatalf("a plain npm layout must not gain a store bind: %q", root)
	}
}

func TestDeniedHostsAreCappedAndShaped(t *testing.T) {
	for _, bad := range []string{"bad host", "x;rm", strings.Repeat("a", 254)} {
		if deniedHostGrammar.MatchString(bad) {
			t.Fatalf("%q must not match the hostname grammar", bad)
		}
	}
	for _, good := range []string{"api.anthropic.com", "localhost", strings.Repeat("a", 253)} {
		if !deniedHostGrammar.MatchString(good) {
			t.Fatalf("%q must match the hostname grammar", good)
		}
	}
	p := &egressProxy{}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go io.Copy(io.Discard, client)
	p.denyRequest(server, "bad host")
	for i := 0; i < deniedHostLimit+5; i++ {
		p.denyRequest(server, fmt.Sprintf("host%d.example", i))
	}
	got := p.denials()
	if len(got) != deniedHostLimit {
		t.Fatalf("the denied list must stay capped at %d: %v", deniedHostLimit, got)
	}
	if containsFold(got, "bad host") {
		t.Fatalf("a name outside the hostname grammar reached the denied list: %v", got)
	}
}

func TestASymlinkedLoginKeepsItsHarnessVisibleRunPath(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "dotfiles", "sesh"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "dotfiles", "sesh", "credentials.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dotfiles", "sesh", "key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".sesh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".sesh", "credentials.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "dotfiles", "sesh", "key"), filepath.Join(home, ".sesh", "key")); err != nil {
		t.Fatal(err)
	}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "true", timeout: time.Minute, role: &harness.Role{Command: "true", Profile: "sesh"}, home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if len(spec.logins) != 2 {
		t.Fatalf("sesh must bind two login files: %v", spec.logins)
	}
	first := spec.logins[0]
	if first.run != filepath.Join(spec.runHome, ".sesh", "credentials.json") {
		t.Fatalf("the run copy must sit at the harness-visible path: %q", first.run)
	}
	if first.real != evalPath(target) {
		t.Fatalf("write-back must target the resolved file: %q", first.real)
	}
	if _, err := os.Stat(first.run); err != nil {
		t.Fatalf("the run copy must exist: %v", err)
	}
}

func TestNodeInstallDirsIncludeTheLibraryFolder(t *testing.T) {
	prefix := t.TempDir()
	for _, d := range []string{"bin", "lib"} {
		if err := os.MkdirAll(filepath.Join(prefix, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	node := filepath.Join(prefix, "bin", "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := nodeInstallDirs(node)
	if !slices.Contains(dirs, evalPath(filepath.Join(prefix, "lib"))) {
		t.Fatalf("dirs = %v; node loads libnode from ../lib and aborts without it", dirs)
	}
}

func TestALoginUnderAHomeReachedThroughASymlinkIsAccepted(t *testing.T) {
	realHome := t.TempDir()
	home := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(realHome, home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	if err := os.MkdirAll(filepath.Join(realHome, "dotfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realHome, ".sesh"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials.json", "key"} {
		if err := os.WriteFile(filepath.Join(realHome, "dotfiles", name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, "dotfiles", name), filepath.Join(realHome, ".sesh", name)); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "true", timeout: time.Minute, role: &harness.Role{Command: "true", Profile: "sesh"}, home: home})
	if spec.runDir != "" {
		defer removeTree(spec.runDir)
	}
	if spec.sockDir != "" {
		defer removeTree(spec.sockDir)
	}
	if err != nil {
		t.Fatalf("a login inside a home reached through a symlink was refused: %v", err)
	}
}

func TestAnOutsideLoginIsNamedByItsHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	outside := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".sesh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".sesh", "credentials.json")); err != nil {
		t.Fatal(err)
	}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "true", timeout: time.Minute, role: &harness.Role{Command: "true", Profile: "sesh"}, home: home})
	if spec.runDir != "" {
		defer removeTree(spec.runDir)
	}
	if spec.sockDir != "" {
		defer removeTree(spec.sockDir)
	}
	if err == nil || !strings.Contains(err.Error(), "~/.sesh/credentials.json resolves to") {
		t.Fatalf("err = %v; the message must name the login by its path in the home", err)
	}
}

func TestAProgramReachedThroughALinkedFolderKeepsItsLinkTraversable(t *testing.T) {
	store := t.TempDir()
	realPkg := filepath.Join(store, "content", "node_modules", "@pnpm", "exe")
	if err := os.MkdirAll(realPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(store, "v11")
	if err := os.Symlink(filepath.Join(store, "content"), linked); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(linked, "node_modules", "@pnpm", "exe")
	s := seatbeltPaths(sandboxSpec{programDirs: []string{program}, personHome: t.TempDir()})
	profile := seatbeltProfile(s, "18080")
	if !strings.Contains(profile, "(path-ancestors "+strconv.Quote(program)+")") {
		t.Fatalf("the profile resolves %s to its target only, so the link %s cannot be traversed and the program cannot start:\n%s", program, linked, profile)
	}
}

func TestHomebrewLibrariesANodeLoadsAreBound(t *testing.T) {
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "libuv", "1.51", "lib")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "opt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(cellar), filepath.Join(root, "opt", "libuv")); err != nil {
		t.Fatal(err)
	}
	libuv := filepath.Join(root, "opt", "libuv", "lib", "libuv.1.dylib")
	icu := filepath.Join(root, "opt", "icu4c", "lib", "libicuuc.dylib")
	node := filepath.Join(root, "node")
	old := importedLibraries
	importedLibraries = func(path string) []string {
		switch path {
		case node:
			return []string{"/usr/lib/libSystem.B.dylib", "@rpath/libnode.dylib", libuv}
		case evalPath(libuv):
			return []string{icu}
		}
		return nil
	}
	t.Cleanup(func() { importedLibraries = old })
	etc := filepath.Join(root, "etc", "libuv")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := dylibDirs(node)
	for _, want := range []string{filepath.Dir(libuv), evalPath(cellar), filepath.Dir(icu), etc} {
		if !slices.Contains(dirs, want) {
			t.Errorf("dirs = %v; missing %s, so dyld cannot load it inside the sandbox", dirs, want)
		}
	}
	for _, d := range dirs {
		if strings.HasPrefix(d, "/usr/lib") || strings.HasPrefix(d, "@") {
			t.Errorf("dirs = %v; system and @rpath libraries must not be bound", dirs)
		}
	}
}

func TestAProgramInsideAWorkedRepoIsRefused(t *testing.T) {
	home := t.TempDir()
	repo := gitClone(t)
	marker := filepath.Join(t.TempDir(), "ran")
	program := filepath.Join(repo, "go")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: program + " env"}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: role.Command, timeout: time.Minute, role: &role, repo: repo, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err == nil || !strings.Contains(err.Error(), "which ghafk works on") {
		t.Fatalf("a program inside the worked repository must be refused: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("setting up the sandbox ran a repository program outside it")
	}
}

func TestGoRootNeverRunsTheProgram(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("go1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goBin := filepath.Join(root, "bin", "go")
	if err := os.WriteFile(goBin, []byte("#!/bin/sh\ntouch "+marker+"\necho /\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := goRoot(goBin); got != root {
		t.Fatalf("goRoot = %q, want %q", got, root)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("goRoot ran the go program")
	}
	if dirs := programBindDirs(goBin); !slices.Contains(dirs, root) && !slices.Contains(dirs, evalPath(root)) {
		t.Fatalf("the go installation root must be bound: %v", dirs)
	}
}

func TestAShimTargetOutsideItsInstallIsIgnored(t *testing.T) {
	install := t.TempDir()
	private := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(install, "bin", "tool")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n# cmd-shim-target="+filepath.Join(private, "node_modules", "x", "cli.js")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range programBindDirs(shim) {
		if strings.HasPrefix(evalPath(dir), evalPath(filepath.Dir(private))) {
			t.Fatalf("a shim pointing outside its install exposed %s", dir)
		}
	}
	inside := filepath.Join(install, "global", "node_modules", "x", "cli.js")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n# cmd-shim-target="+inside+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(programBindDirs(shim), "\n"); !strings.Contains(joined, evalPath(filepath.Dir(inside))) {
		t.Fatalf("a shim target inside its install must be bound:\n%s", joined)
	}
}

func TestTheMacOSProfileReadsTheRepoGitWithoutExec(t *testing.T) {
	git := filepath.Join(t.TempDir(), "repo", ".git")
	profile := seatbeltProfile(seatbeltPaths(sandboxSpec{repoGit: git, personHome: t.TempDir()}), "1234")
	want := "(subpath " + strconv.Quote(evalPath(git)) + ")"
	var readRule, execRule string
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(line, "(allow file-read* ") && strings.Contains(line, want) {
			readRule = line
		}
		if strings.HasPrefix(line, "(allow process-exec ") && strings.Contains(line, want) {
			execRule = line
		}
	}
	if readRule == "" {
		t.Fatalf("the repository .git must be readable on macOS:\n%s", profile)
	}
	if execRule != "" {
		t.Fatalf("the repository .git must not be executable: %s", execRule)
	}
}

func TestAnAPIKeyAloneIsALogin(t *testing.T) {
	for profile, key := range map[string]string{"codex": "OPENAI_API_KEY", "pi": "ANTHROPIC_API_KEY", "opencode": "OPENROUTER_API_KEY", "sesh": "OPENAI_API_KEY"} {
		for _, k := range sandboxModelEnvKeys {
			t.Setenv(k, "")
		}
		t.Setenv("CODEX_API_KEY", "")
		if _, err := sandboxLoginFiles(profile, t.TempDir()); err == nil {
			t.Fatalf("%s with no login and no key must be refused", profile)
		}
		t.Setenv(key, "dummy-key")
		if _, err := sandboxLoginFiles(profile, t.TempDir()); err != nil {
			t.Fatalf("%s with %s set must run: %v", profile, key, err)
		}
	}
}

func TestABindDirectoryResolvesAProgramAndJoinsThePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	t.Setenv(configDirEnv, filepath.Join(home, ".ghafk"))
	tools := filepath.Join(t.TempDir(), "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "review-tool-zz"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ghafk"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ghafk", "config"), []byte("bind: "+tools+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := buildSandbox(sandboxOpts{name: "checks", dir: t.TempDir(), command: "review-tool-zz --all", timeout: time.Minute, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err != nil {
		t.Fatalf("a program in a bind directory must resolve: %v", err)
	}
	if !strings.Contains(spec.path, tools) {
		t.Fatalf("the bind directory must be on the sandbox PATH: %s", spec.path)
	}
}

func TestBindRefusesDirectoriesThatHoldLogins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	for _, dir := range []string{"/", home, filepath.Dir(home), filepath.Join(home, ".ssh"), filepath.Join(home, ".ssh", "keys"), filepath.Join(home, ".codex", "sessions"), filepath.Join(home, ".config", "opencode"), filepath.Join(home, ".config", "gh"), filepath.Join(home, ".config"), filepath.Join(home, ".local", "share")} {
		if unsafeBind(dir) == "" {
			t.Errorf("bind %s must be refused", dir)
		}
	}
	for _, dir := range []string{filepath.Join(home, ".local", "share", "pnpm"), filepath.Join(home, ".nvm", "versions"), "/opt/tools/bin"} {
		if reason := unsafeBind(dir); reason != "" {
			t.Errorf("bind %s must be allowed: %s", dir, reason)
		}
	}
}

func TestStaleRunDirectoriesAreSwept(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Join(home, ".ghafk", "run")
	old, fresh := filepath.Join(parent, "old"), filepath.Join(parent, "fresh")
	for _, dir := range []string{old, fresh} {
		if err := os.MkdirAll(filepath.Join(dir, "home", ".codex"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * staleRunAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	sweepStaleRuns(home, time.Now())
	if _, err := os.Stat(old); err == nil {
		t.Fatal("a run directory left by a killed tick must be swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a run directory that may still be in use must stay")
	}
}

func TestAProgramThatWouldExposeTheHomeIsRefused(t *testing.T) {
	home := t.TempDir()
	direct := filepath.Join(home, "my-agent")
	if err := os.WriteFile(direct, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: direct}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: direct, timeout: time.Minute, role: &role, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err == nil || !strings.Contains(err.Error(), "would expose your whole home") {
		t.Fatalf("a program directly in the home must be refused: %v", err)
	}
}

func TestBindRefusesALinkToASecretDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "tools")
	if err := os.Symlink(filepath.Join(home, ".ssh"), alias); err != nil {
		t.Fatal(err)
	}
	if unsafeBind(alias) == "" {
		t.Fatal("a bind line that is a link to ~/.ssh must be refused")
	}
}

func TestTheWorktreePointerCheckDoesNotBlockOnAPipe(t *testing.T) {
	work := t.TempDir()
	admin := filepath.Join(t.TempDir(), "admin")
	worktreeGitDirs[work] = admin
	worktreePointers[work] = "gitdir: " + admin
	defer delete(worktreeGitDirs, work)
	defer delete(worktreePointers, work)
	if err := syscall.Mkfifo(filepath.Join(work, ".git"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worktreeIntact(work) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a pipe in place of the git pointer must fail the check")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pointer check blocked on a pipe; the tick would hang")
	}
}

func TestAProgramInTheSharedTempDirectoryIsRefused(t *testing.T) {
	for _, dir := range []string{"/tmp", "/run", "/var/tmp"} {
		if exposesSecrets(dir, t.TempDir()) == "" {
			t.Errorf("binding %s must be refused", dir)
		}
	}
}

func TestLoaderVariablesNeverReachTheLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	t.Setenv(configDirEnv, filepath.Join(home, ".ghafk"))
	if err := os.MkdirAll(filepath.Join(home, ".ghafk"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LD_PRELOAD", "LD_AUDIT", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "GCONV_PATH"} {
		if err := os.WriteFile(filepath.Join(home, ".ghafk", "config"), []byte("env: "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadMachineSettings(); err == nil {
			t.Errorf("env: %s must be refused", name)
		}
		if sandboxEnvKey(name, "claude", []string{name}) {
			t.Errorf("%s would reach the environment of the sandbox launcher", name)
		}
	}
}

func syncBack(t *testing.T, spec sandboxSpec) {
	t.Helper()
	root, err := os.OpenRoot(spec.runHome)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	loginSyncBack(spec, root)
}

func TestCopyBackUsesTheRunHomeOpenedBeforeTheRun(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "auth.json")
	orig := []byte(`{"tokens":{"refresh_token":"old-refresh-token-value"}}`)
	if err := os.WriteFile(real, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	runHome := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(runHome, 0o700); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(runHome, "auth.json")
	if err := os.WriteFile(run, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(runHome)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(runHome, runHome+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(run, []byte(`{"tokens":{"refresh_token":"OUTSIDE-hidden-login-value"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := sandboxSpec{runHome: runHome, logins: []loginSeed{{real: real, run: run, runResolved: run, orig: orig}}}
	loginSyncBack(spec, root)
	data, _ := os.ReadFile(real)
	if strings.Contains(string(data), "OUTSIDE") {
		t.Fatal("copy-back read a run home that was replaced after the run started")
	}
}

func TestCopyBackDoesNotBlockOnAPipe(t *testing.T) {
	runHome := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(runHome, "auth.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(runHome)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan bool, 1)
	go func() {
		_, ok := readSeedFile(root, "auth.json")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a pipe must not be read as a login")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy-back blocked on a pipe")
	}
}

func TestRemovingARunNeverFollowsALinkOutOfIt(t *testing.T) {
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o400); err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(runDir, "escape")); err != nil {
		t.Fatal(err)
	}
	removeTree(runDir)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup followed a link out of the run folder: %v", err)
	}
	if info, _ := os.Stat(keep); info != nil && info.Mode().Perm() != 0o400 {
		t.Fatalf("cleanup changed the mode of a file outside the run folder: %v", info.Mode())
	}
	if info, _ := os.Stat(outside); info == nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("cleanup changed the mode of a folder a link pointed to: %v", info)
	}
	if _, err := os.Lstat(runDir); !os.IsNotExist(err) {
		t.Fatalf("the run folder was not removed: %v", err)
	}
}

func TestClaudeCanRefreshItsLoginThroughTheProxy(t *testing.T) {
	if !slices.Contains(egressList("claude", nil), "platform.claude.com") {
		t.Fatal("Claude Code refreshes its login at platform.claude.com; without it a subscription login expires inside the sandbox")
	}
}

func TestSubscriptionLoginsCanRefreshForEveryBroadHarness(t *testing.T) {
	for _, profile := range []string{"pi", "opencode", "sesh"} {
		for _, host := range []string{"platform.claude.com", "console.anthropic.com", "chatgpt.com", "auth.openai.com"} {
			if !hostAllowed(host, egressList(profile, nil)) {
				t.Errorf("%s cannot reach %s, so a subscription login cannot refresh inside the sandbox", profile, host)
			}
		}
	}
}

func TestChecksRunsDoNotShareTheAgentCache(t *testing.T) {
	if !sharedSandboxCache() {
		t.Skip("every run gets its own cache on this platform")
	}
	home := t.TempDir()
	repo := t.TempDir()
	agent, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, repo: repo, role: &harness.Role{Profile: "custom", Command: "/bin/true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(agent.runDir)
	defer removeTree(agent.sockDir)
	checks, err := buildSandbox(sandboxOpts{name: "checks", dir: t.TempDir(), home: home, repo: repo, command: "true"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(checks.runDir)
	defer removeTree(checks.sockDir)
	if agent.cacheDir == checks.cacheDir {
		t.Fatalf("checks share the agent cache %s, so a worker can leave a poisoned dependency that the checks compile", agent.cacheDir)
	}
}

func TestTheMacProfileRunsTheDeveloperTools(t *testing.T) {
	profile := seatbeltProfile(seatbeltPaths(sandboxSpec{personHome: "/Users/u", workDir: "/w", runHome: "/r/home", runDir: "/r", cacheDir: "/r/cache"}), "1")
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(line, "(allow process-exec ") && strings.Contains(line, `(subpath "/Library/Developer/CommandLineTools")`) {
			return
		}
	}
	t.Fatalf("git, make and python3 on macOS are shims that run the Command Line Tools, which the profile must allow to run:\n%s", profile)
}

func TestTheMacProfileReadsTheSelectedXcode(t *testing.T) {
	contents := filepath.Join(t.TempDir(), "Xcode.app", "Contents")
	dev := filepath.Join(contents, "Developer")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "xcode_select_link")
	if err := os.Symlink(dev, link); err != nil {
		t.Fatal(err)
	}
	saved := xcodeSelectLink
	xcodeSelectLink = link
	defer func() { xcodeSelectLink = saved }()
	s := seatbeltPaths(sandboxSpec{personHome: "/Users/u", workDir: "/w", runHome: "/r/home", runDir: "/r", cacheDir: "/r/cache"})
	if !slices.Contains(s.Reads, evalPath(contents)) || !slices.Contains(s.Execs, evalPath(dev)) {
		t.Fatalf("the git shim reads the selected Xcode's Info.plist and runs its tools: reads %v, execs %v", s.SystemReads, s.Execs)
	}
}

func TestTheMacProfileAllowsOnlyTheLocalPortsTheConfigNames(t *testing.T) {
	profile := seatbeltProfile(seatbeltSpec{LocalPorts: []int{11434}}, "3128")
	if !strings.Contains(profile, `(remote tcp "localhost:11434")`) {
		t.Fatalf("a local: port is missing from the profile:\n%s", profile)
	}
	if strings.Contains(seatbeltProfile(seatbeltSpec{}, "3128"), "11434") {
		t.Fatal("a local port must be reachable only when the config names it")
	}
}

func TestLocalPortsParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("local: 11434 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadSettings(path)
	if err != nil || !slices.Equal(cfg.local, []int{11434, 8080}) {
		t.Fatalf("local = %v, %v", cfg.local, err)
	}
	for _, bad := range []string{"local: 0", "local: 70000", "local: ollama", "local: 127.0.0.1:11434"} {
		if err := os.WriteFile(path, []byte(bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSettings(path); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestRedactionIsLogged(t *testing.T) {
	registerSecret("ghafk-dummy-secret-value-0123456789")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	redactArgs([]string{"issue", "comment", "--body", "leak ghafk-dummy-secret-value-0123456789"})
	os.Stderr = saved
	w.Close()
	logged, _ := io.ReadAll(r)
	if !strings.Contains(string(logged), "removed 1 value") {
		t.Fatalf("a redaction must show in the tick log, got %q", logged)
	}
}

func TestAVersionManagerShimBindsItsRoot(t *testing.T) {
	root := evalPath(t.TempDir())
	os.MkdirAll(filepath.Join(root, "shims"), 0o755)
	shim := filepath.Join(root, "shims", "tool")
	os.WriteFile(shim, []byte("#!/usr/bin/env bash\nexport PYENV_ROOT=\""+root+"\"\nexec \""+root+"/libexec/pyenv\" exec tool \"$@\"\n"), 0o755)
	if !slices.Contains(programBindShallow(shim), root) {
		t.Fatalf("the shim's version manager root is not bound: %v", programBindShallow(shim))
	}
	other := evalPath(t.TempDir())
	os.MkdirAll(filepath.Join(other, "shims"), 0o755)
	liar := filepath.Join(other, "shims", "tool")
	os.WriteFile(liar, []byte("#!/usr/bin/env bash\nexport PYENV_ROOT=\""+root+"\"\n"), 0o755)
	if dirs := programBindShallow(liar); slices.Contains(dirs, root) || slices.Contains(dirs, other) {
		t.Fatalf("a shim naming another root must bind neither: %v", dirs)
	}
}
