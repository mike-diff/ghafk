package main

import (
	"bytes"
	"debug/macho"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

type sandboxOpts struct {
	name    string
	dir     string
	command string
	stdin   string
	timeout time.Duration
	role    *harness.Role
	egress  []string
	repo    string
	home    string
}

type loginSeed struct {
	real        string
	run         string
	runResolved string
	orig        []byte
}

type sandboxSpec struct {
	passEnv       []string
	machineEgress []string
	personHome    string
	homeHide      string
	homeReal      string
	runDir        string
	runHome       string
	sockDir       string
	cacheDir      string
	workDir       string
	repoGit       string
	runID         string
	logins        []loginSeed
	programs      []string
	programDirs   []string
	extraBinds    []string
	localPorts    []int
	ghafkBin      string
	path          string
	profile       string
}

var sandboxLogins = map[string][]string{
	"claude":   {".claude/.credentials.json"},
	"codex":    {".codex/auth.json"},
	"pi":       {".pi/agent/auth.json"},
	"sesh":     {".sesh/credentials.json", ".sesh/key"},
	"opencode": {".local/share/opencode/auth.json", ".config/opencode/auth.json"},
}

var sandboxConfigs = map[string][]string{
	"codex":    {".codex/config.toml"},
	"pi":       {".pi/agent/models.json"},
	"opencode": {".config/opencode"},
	"sesh":     {".sesh/providers.json"},
}

var sandboxCommonEnv = []string{"TERM", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR"}

var sandboxModelEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENROUTER_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"MISTRAL_API_KEY",
	"XAI_API_KEY",
	"GROQ_API_KEY",
	"DEEPSEEK_API_KEY",
	"COHERE_API_KEY",
}

var sandboxEnvAllow = map[string][]string{
	"claude":   {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"},
	"codex":    {"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL"},
	"pi":       sandboxModelEnvKeys,
	"sesh":     sandboxModelEnvKeys,
	"opencode": sandboxModelEnvKeys,
}

var registryEgress = []string{
	"registry.npmjs.org",
	"proxy.golang.org",
	"sum.golang.org",
	"pypi.org",
	"files.pythonhosted.org",
}

var sandboxModelEgress = map[string][]string{
	"claude":   {"api.anthropic.com", "claude.ai", "platform.claude.com"},
	"codex":    {"api.openai.com", "chatgpt.com", "auth.openai.com"},
	"pi":       append([]string{"models.dev"}, broadModelEgress...),
	"sesh":     append([]string{"models.dev"}, broadModelEgress...),
	"opencode": append([]string{"models.dev", "opencode.ai", "models.opencode.ai"}, broadModelEgress...),
}

var broadModelEgress = []string{
	"api.anthropic.com",
	"platform.claude.com",
	"console.anthropic.com",
	"api.openai.com",
	"chatgpt.com",
	"auth.openai.com",
	"openrouter.ai",
	"generativelanguage.googleapis.com",
	"api.mistral.ai",
	"api.x.ai",
	"api.deepseek.com",
	"api.groq.com",
	"api.cohere.com",
	"api.z.ai",
	"open.bigmodel.cn",
	"api.moonshot.ai",
	"api.moonshot.cn",
	"api.kimi.com",
	"api.together.ai",
	"api.fireworks.ai",
	"api.cerebras.ai",
	"api.minimax.io",
	"router.huggingface.co",
	"integrate.api.nvidia.com",
	"inference.baseten.co",
	"ai-gateway.vercel.sh",
}

type sandboxSetupError struct{ err error }

func (e sandboxSetupError) Error() string { return e.err.Error() }
func (e sandboxSetupError) Unwrap() error { return e.err }

func setupFailed(format string, args ...any) error {
	return sandboxSetupError{fmt.Errorf(format, args...)}
}

func runSandboxed(o sandboxOpts, stdout, stderr io.Writer) error {
	spec, err := buildSandbox(o)
	if err != nil {
		removeTree(spec.runDir)
		removeTree(spec.sockDir)
		return setupFailed("%s: %w", o.name, err)
	}
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	loginRoot, err := os.OpenRoot(spec.runHome)
	if err != nil {
		return setupFailed("%s: %w", o.name, err)
	}
	defer loginRoot.Close()
	proxy := &egressProxy{allowed: egressList(spec.profile, append(append([]string{}, spec.machineEgress...), o.egress...))}
	if err := proxyStart(proxy, spec); err != nil {
		return setupFailed("%s: the egress proxy did not start: %w", o.name, err)
	}
	defer proxy.close()
	if err := proxy.check(); err != nil {
		return setupFailed("%s: the egress proxy did not answer: %w", o.name, err)
	}
	if err := sandboxReady(); err != nil {
		return setupFailed("%s: %w", o.name, err)
	}
	if err := probeHarnessProgram(o, spec, proxy); err != nil {
		return setupFailed("%s: %w", o.name, err)
	}
	err = sandboxLaunch(o, spec, proxy, sandboxCommand(o.command, spec.profile), stdout, stderr)
	if denied := proxy.denials(); err == nil && len(denied) > 0 {
		fmt.Fprintf(stderr, "ghafk: %s: the sandbox proxy denied: %s\n", o.name, strings.Join(denied, ", "))
	}
	loginSyncBack(spec, loginRoot)
	if perr := worktreeIntact(spec.workDir); perr != nil {
		if err != nil {
			err = fmt.Errorf("%w\n%v", err, perr)
		} else {
			err = setupFailed("%s: %w", o.name, perr)
		}
	}
	return decorateRunError(err, proxy)
}

func decorateRunError(err error, proxy *egressProxy) error {
	if err == nil {
		return nil
	}
	if denied := proxy.denials(); len(denied) > 0 {
		return fmt.Errorf("%w"+deniedMarker+"%s", err, strings.Join(denied, ", "))
	}
	return err
}

func probeHarnessProgram(o sandboxOpts, spec sandboxSpec, proxy *egressProxy) error {
	if o.role == nil || len(spec.programs) == 0 {
		return nil
	}
	probe := o
	probe.timeout = 90 * time.Second
	probe.stdin = ""
	probe.command = strconv.Quote(spec.programs[0]) + " --version >/dev/null; echo probe:$?"
	var out bytes.Buffer
	if err := sandboxLaunch(probe, spec, proxy, probe.command, &out, &out); err != nil {
		return fmt.Errorf("the harness program %q could not be probed inside the sandbox: %v: %s", spec.programs[0], err, firstLine(out.String()))
	}
	fields := strings.Fields(out.String())
	code := -1
	if len(fields) > 0 {
		if tagged, ok := strings.CutPrefix(fields[len(fields)-1], "probe:"); ok {
			code, _ = strconv.Atoi(tagged)
		}
	}
	tail := strings.ToLower(strings.Join(fields, " "))
	if code == 126 || code == 127 || strings.Contains(tail, "not found") || strings.Contains(tail, "permission denied") || strings.Contains(tail, "bad interpreter") {
		return fmt.Errorf("the harness program %q does not start inside the sandbox (exit %d). Install it system-wide, or bind the directory it lives in with a `bind:` line in ~/.ghafk/config; do not bind your whole home or ~/.local/share", spec.programs[0], code)
	}
	return nil
}

const loginSizeLimit = 1 << 20

func loginSyncBack(spec sandboxSpec, root *os.Root) {
	for _, seed := range spec.logins {
		if seed.runResolved == "" || evalPath(seed.run) != seed.runResolved {
			continue
		}
		rel, err := filepath.Rel(spec.runHome, seed.run)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		data, ok := readSeedFile(root, rel)
		if !ok || bytes.Equal(data, seed.orig) {
			continue
		}
		if v, err := decodeLoginJSON(data); err == nil {
			registerTokenFields(v, "")
		}
		if now, err := os.ReadFile(seed.real); err != nil || !bytes.Equal(now, seed.orig) {
			continue
		}
		merged, ok := mergeLoginRefresh(seed.orig, data)
		if !ok || bytes.Equal(merged, seed.orig) {
			continue
		}
		writeLoginFile(seed.real, merged)
	}
}

func writeLoginFile(path string, data []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ghafk-login-")
	if err != nil {
		return
	}
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil && cerr == nil && os.Chmod(tmp.Name(), mode) == nil && os.Rename(tmp.Name(), path) == nil {
		return
	}
	os.Remove(tmp.Name())
}

func readSeedFile(root *os.Root, rel string) ([]byte, bool) {
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() > loginSizeLimit {
		return nil, false
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	fdInfo, err := f.Stat()
	if err != nil || !os.SameFile(fdInfo, info) {
		return nil, false
	}
	if st, ok := fdInfo.Sys().(*syscall.Stat_t); !ok || st.Nlink != 1 {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, loginSizeLimit+1))
	if err != nil || len(data) > loginSizeLimit {
		return nil, false
	}
	return data, true
}

var loginTokenFields = map[string]bool{
	"access": true, "refresh": true, "expires": true,
	"accessToken": true, "refreshToken": true, "expiresAt": true,
	"access_token": true, "refresh_token": true, "id_token": true,
	"expires_at": true, "last_refresh": true,
}

func mergeLoginRefresh(orig, changed []byte) ([]byte, bool) {
	before, err := decodeLoginJSON(orig)
	if err != nil {
		return nil, false
	}
	after, err := decodeLoginJSON(changed)
	if err != nil {
		return nil, false
	}
	merged := mergeTokenFields(before, after, "")
	if reflect.DeepEqual(merged, before) {
		return orig, true
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(out, '\n'), true
}

func decodeLoginJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data")
	}
	return v, nil
}

func mergeTokenFields(before, after any, key string) any {
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			return before
		}
		out := make(map[string]any, len(b))
		for k, v := range b {
			if nv, present := a[k]; present {
				out[k] = mergeTokenFields(v, nv, k)
			} else {
				out[k] = v
			}
		}
		return out
	case string, json.Number:
		if !loginTokenFields[key] {
			return before
		}
		switch v := after.(type) {
		case json.Number:
			if _, isNum := before.(json.Number); isNum {
				return v
			}
		case string:
			if _, isStr := before.(string); isStr && safeTokenValue(v) {
				return v
			}
		}
		return before
	default:
		return before
	}
}

func safeTokenValue(v string) bool {
	if v == "" || len(v) > 16<<10 || strings.HasPrefix(v, "!") || strings.ContainsAny(v, "$`{}") {
		return false
	}
	for _, r := range v {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

func egressList(profile string, extra []string) []string {
	allowed := append([]string{}, registryEgress...)
	allowed = append(allowed, sandboxModelEgress[profile]...)
	for _, host := range extra {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" && !containsFold(allowed, host) {
			allowed = append(allowed, host)
		}
	}
	return allowed
}

func buildSandbox(o sandboxOpts) (sandboxSpec, error) {
	spec := sandboxSpec{workDir: o.dir}
	if o.role != nil {
		spec.profile = o.role.Profile
	}
	home := o.home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return spec, err
		}
	}
	spec.personHome = home
	spec.homeHide = home
	if real := evalPath(home); real != home {
		spec.homeReal = real
	}
	if parent := filepath.Dir(home); parent == "/home" || parent == "/Users" {
		spec.homeHide = parent
	}
	if err := os.MkdirAll(filepath.Join(home, ".ghafk", "run"), 0o700); err != nil {
		return spec, err
	}
	runDir, err := os.MkdirTemp(filepath.Join(home, ".ghafk", "run"), "")
	if err != nil {
		return spec, err
	}
	spec.runDir = runDir
	spec.runID = filepath.Base(runDir)
	spec.runHome = filepath.Join(spec.runDir, "home")
	spec.sockDir, err = newSocketDir()
	if err != nil {
		return spec, err
	}
	if err := os.MkdirAll(filepath.Join(spec.runHome, "tmp"), 0o700); err != nil {
		return spec, err
	}
	if o.repo != "" && sharedSandboxCache() {
		spec.cacheDir = filepath.Join(home, ".ghafk", "cache", repoSlug(o.repo)+cacheRole(o.role))
	} else {
		spec.cacheDir = filepath.Join(spec.runDir, "cache")
	}
	for _, sub := range sharedCacheDirs() {
		if err := os.MkdirAll(filepath.Join(spec.cacheDir, sub), 0o755); err != nil {
			return spec, err
		}
	}
	for _, sub := range runHomeDirs() {
		if err := os.MkdirAll(filepath.Join(spec.runHome, sub), 0o755); err != nil {
			return spec, err
		}
	}
	logins, err := sandboxLoginFiles(spec.profile, home)
	if err != nil {
		return spec, err
	}
	for _, path := range logins {
		target := evalPath(path)
		rel, err := filepath.Rel(evalPath(home), target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			shown, _ := filepath.Rel(home, path)
			return spec, fmt.Errorf("the login file ~/%s resolves to %s, outside your home. Point the link at a file inside your home", shown, target)
		}
		seed, ok := seedIntoRunHome(path, home, spec.runHome)
		if !ok {
			continue
		}
		registerSecretFile(path)
		seed.real = target
		spec.logins = append(spec.logins, seed)
	}
	for _, rel := range sandboxConfigs[spec.profile] {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if seed, ok := seedIntoRunHome(path, home, spec.runHome); ok {
			registerConfigSecrets(seed.run)
		}
	}
	words := []string{}
	if o.role != nil {
		words = append(words, firstWord(sandboxCommand(o.role.Command, spec.profile)))
	} else {
		words = append(words, commandWords(o.command)...)
	}
	cfg, err := loadMachineSettings()
	if err != nil {
		return spec, err
	}
	spec.passEnv = cfg.env
	spec.machineEgress = cfg.egress
	spec.localPorts = cfg.local
	missing := []string{}
	for _, w := range words {
		if w == "" {
			continue
		}
		path, lerr := lookPathIn(w, cfg.bind)
		if lerr != nil {
			missing = append(missing, w)
			continue
		}
		if tree := workedTree(evalPath(path), home, o); tree != "" {
			return spec, fmt.Errorf("the program %q is inside %s, which ghafk works on, so that repository could decide what runs outside the sandbox. Name a program installed on your PATH instead", w, tree)
		}
		spec.programs = append(spec.programs, path)
	}
	if len(missing) > 0 {
		what := "the harness program"
		if o.role == nil {
			what = "the checks command names"
		}
		return spec, fmt.Errorf("%s %s, which is not on PATH. Install it, or bind the directory it lives in with a `bind:` line in ~/.ghafk/config; do not bind your whole home or ~/.local/share", what, quoteList(missing))
	}
	for _, bin := range spec.programs {
		for _, dir := range programBindDirs(bin) {
			if reason := exposesSecrets(dir, home); reason != "" {
				return spec, fmt.Errorf("the program %s needs the directory %s, which %s. Install the program in a directory of its own", bin, dir, reason)
			}
			spec.programDirs = append(spec.programDirs, dir)
		}
	}
	if o.dir != "" {
		if admin, ok := worktreeGitDirs[o.dir]; ok {
			spec.repoGit = filepath.Dir(filepath.Dir(admin))
		}
	}
	if exe, err := os.Executable(); err == nil {
		spec.ghafkBin = evalPath(exe)
	}
	spec.extraBinds = append(spec.extraBinds, cfg.bind...)
	spec.programDirs = dedup(spec.programDirs)
	spec.path = sandboxPath(append(append([]string{}, spec.programDirs...), cfg.bind...), sandboxFirstPath())
	return spec, nil
}

func sharedCacheDirs() []string {
	return []string{"go-build", "go-mod", "npm", "pnpm", "pnpm-cache"}
}

func runHomeDirs() []string {
	return []string{"go", filepath.FromSlash(".cargo"), filepath.FromSlash(".cache/pip"), filepath.FromSlash(".cache/uv")}
}

const socketPathLimit = 100

func cacheRole(role *harness.Role) string {
	if role == nil {
		return "-checks"
	}
	return ""
}

func newSocketDir() (string, error) {
	for _, parent := range []string{os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(), "/tmp"} {
		if parent == "" || len(proxySocketPath(filepath.Join(parent, "ghafk-0123456789"))) > socketPathLimit {
			continue
		}
		if _, err := os.Stat(parent); err == nil {
			return os.MkdirTemp(parent, "ghafk-")
		}
	}
	return "", fmt.Errorf("no folder for the sandbox socket keeps its path under %d characters; set TMPDIR to a short folder", socketPathLimit)
}

const staleRunAge = 24 * time.Hour

func sweepStaleRuns(home string, now time.Time) {
	parent := filepath.Join(home, ".ghafk", "run")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && entry.IsDir() && now.Sub(info.ModTime()) > staleRunAge {
			removeTree(filepath.Join(parent, entry.Name()))
		}
	}
}

func removeTree(path string) {
	if path == "" {
		return
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return
	}
	defer parent.Close()
	name := filepath.Base(path)
	if root, err := parent.OpenRoot(name); err == nil {
		_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink == 0 {
				_ = root.Chmod(p, 0o700)
			}
			return nil
		})
		root.Close()
	}
	_ = parent.RemoveAll(name)
}

const toolchainMarker = "/pkg/mod/golang.org/toolchain@"

func programBindDirs(bin string) []string {
	dirs := programBindShallow(bin)
	bin = evalPath(bin)
	if target := shimTarget(bin); target != "" && target != bin && shimTargetTrusted(bin, target) {
		dirs = append(dirs, programBindShallow(target)...)
		bin = target
	}
	if root := pnpmStoreRoot(evalPath(bin)); root != "" {
		dirs = append(dirs, root)
	}
	return dedup(dirs)
}

func linkedProgramBind(resolved string) string {
	dir := filepath.Dir(resolved)
	switch filepath.Base(dir) {
	case "bin", "sbin", ".bin":
		return dir
	}
	if strings.Contains(filepath.ToSlash(resolved), "/node_modules/") {
		return dir
	}
	return resolved
}

func pnpmStoreRoot(resolved string) string {
	slash := filepath.ToSlash(resolved)
	i := strings.Index(slash, "/.pnpm/")
	if i < 0 {
		return ""
	}
	return filepath.FromSlash(slash[:i])
}

func programBindShallow(bin string) []string {
	var dirs []string
	resolved := bin
	if r := evalPath(bin); r != bin {
		resolved = r
		dirs = append(dirs, filepath.Dir(bin))
		dirs = append(dirs, linkedProgramBind(resolved))
	} else {
		dirs = append(dirs, filepath.Dir(resolved))
	}
	if isNodeProgram(resolved) {
		if pkg := nodePackageDir(resolved); pkg != "" {
			dirs = append(dirs, pkg)
		}
		if nm := nearestNodeModules(resolved); nm != "" {
			dirs = append(dirs, nm)
		}
		if pkg := nodePackageDir(bin); pkg != "" {
			dirs = append(dirs, pkg)
			if nm := nearestNodeModules(bin); nm != "" {
				dirs = append(dirs, nm)
			}
		}
		if node, err := exec.LookPath("node"); err == nil {
			dirs = append(dirs, nodeInstallDirs(node)...)
		}
	}
	if filepath.Base(resolved) == "node" {
		dirs = append(dirs, nodeInstallDirs(resolved)...)
	}
	dirs = append(dirs, dylibDirs(resolved)...)
	if filepath.Base(resolved) == "go" {
		if root := goRoot(resolved); root != "" {
			dirs = append(dirs, root)
		}
	}
	if strings.Contains(filepath.ToSlash(resolved), toolchainMarker) {
		dirs = append(dirs, filepath.Dir(filepath.Dir(resolved)))
	}
	return dedup(dirs)
}

func nodeInstallDirs(node string) []string {
	resolved := evalPath(node)
	bin := filepath.Dir(resolved)
	dirs := []string{bin}
	if lib := filepath.Join(filepath.Dir(bin), "lib"); filepath.Base(bin) == "bin" && !strings.HasPrefix(bin, "/usr/") && !strings.HasPrefix(bin, "/bin") {
		if info, err := os.Stat(lib); err == nil && info.IsDir() {
			dirs = append(dirs, lib)
		}
	}
	return append(dirs, dylibDirs(resolved)...)
}

func dylibDirs(binary string) []string {
	var dirs []string
	seen := map[string]bool{}
	var visit func(path string, depth int)
	visit = func(path string, depth int) {
		if depth > 4 || seen[path] {
			return
		}
		seen[path] = true
		for _, lib := range importedLibraries(path) {
			if !filepath.IsAbs(lib) || strings.HasPrefix(lib, "/usr/lib/") || strings.HasPrefix(lib, "/System/") {
				continue
			}
			resolved := evalPath(lib)
			dirs = append(dirs, filepath.Dir(lib), evalPath(filepath.Dir(lib)), filepath.Dir(resolved))
			if etc := kegConfigDir(lib); etc != "" {
				dirs = append(dirs, etc)
			}
			visit(resolved, depth+1)
		}
	}
	visit(binary, 0)
	return dedup(dirs)
}

func kegConfigDir(lib string) string {
	parts := strings.Split(filepath.ToSlash(lib), "/")
	for i := len(parts) - 3; i > 0; i-- {
		if parts[i] == "opt" && parts[i+2] == "lib" {
			etc := filepath.FromSlash(strings.Join(append(append([]string{}, parts[:i]...), "etc", parts[i+1]), "/"))
			if info, err := os.Stat(etc); err == nil && info.IsDir() {
				return etc
			}
			return ""
		}
	}
	return ""
}

var importedLibraries = func(path string) []string {
	if f, err := macho.Open(path); err == nil {
		defer f.Close()
		libs, _ := f.ImportedLibraries()
		return libs
	}
	if f, err := macho.OpenFat(path); err == nil {
		defer f.Close()
		if len(f.Arches) > 0 {
			libs, _ := f.Arches[0].ImportedLibraries()
			return libs
		}
	}
	return nil
}

func nearestNodeModules(path string) string {
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if filepath.Base(d) == "node_modules" {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

func nodePackageDir(path string) string {
	nm := nearestNodeModules(path)
	if nm == "" {
		return ""
	}
	rel, err := filepath.Rel(nm, filepath.Dir(path))
	if err != nil || rel == "." {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	take := 1
	if strings.HasPrefix(parts[0], "@") && len(parts) > 1 {
		take = 2
	}
	return filepath.Join(nm, filepath.FromSlash(strings.Join(parts[:take], "/")))
}

func lookPathIn(name string, extra []string) (string, error) {
	path, err := exec.LookPath(name)
	if err == nil || strings.ContainsRune(name, os.PathSeparator) {
		return path, err
	}
	for _, dir := range extra {
		candidate := filepath.Join(dir, name)
		if info, serr := os.Stat(candidate); serr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return path, err
}

func goRoot(goBin string) string {
	if filepath.Base(filepath.Dir(goBin)) != "bin" {
		return ""
	}
	root := filepath.Dir(filepath.Dir(goBin))
	for _, mark := range []string{"VERSION", filepath.Join("src", "runtime")} {
		if _, err := os.Stat(filepath.Join(root, mark)); err == nil {
			return root
		}
	}
	return ""
}

func workedTree(program, home string, o sandboxOpts) string {
	trees := []string{filepath.Join(home, ".ghafk"), o.dir, o.repo}
	if repos, err := readRepos(filepath.Join(home, ".ghafk", "repos")); err == nil {
		trees = append(trees, repos...)
	}
	for _, tree := range trees {
		if tree == "" {
			continue
		}
		if underTree(program, evalPath(tree)) {
			return tree
		}
	}
	return ""
}

func shimTargetTrusted(shim, target string) bool {
	install := filepath.Dir(filepath.Dir(shim))
	return strings.Contains(filepath.ToSlash(target), "/node_modules/") &&
		underTree(evalPath(filepath.Dir(target)), evalPath(install))
}

func evalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func shimTarget(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	for _, line := range strings.Split(string(head[:n]), "\n") {
		if target, ok := strings.CutPrefix(strings.TrimSpace(line), "# cmd-shim-target="); ok {
			target = strings.TrimSpace(target)
			if target != "" && filepath.IsAbs(target) {
				return target
			}
		}
	}
	return ""
}

func isNodeProgram(path string) bool {
	if strings.Contains(filepath.ToSlash(path), "/node_modules/") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 256)
	n, _ := f.Read(head)
	line := string(head[:n])
	return strings.HasPrefix(line, "#!") && strings.Contains(strings.SplitN(line, "\n", 2)[0], "node")
}

func sandboxPath(extra []string, first []string) string {
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	for _, dir := range first {
		add(dir)
	}
	for _, dir := range append(extra, filepath.SplitList(os.Getenv("PATH"))...) {
		add(dir)
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		add(dir)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

func sandboxLoginFiles(profile, home string) ([]string, error) {
	if profile == "omp" {
		return nil, fmt.Errorf("omp cannot run inside the ghafk sandbox: its built-in model client ignores the egress proxy, and omp 18 keeps its login in a database ghafk cannot pass. Use the pi profile or an omp version whose client honors HTTPS_PROXY")
	}
	rel := sandboxLogins[profile]
	if len(rel) == 0 {
		return nil, nil
	}
	var found []string
	for _, name := range rel {
		path := filepath.Join(home, filepath.FromSlash(name))
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			found = append(found, path)
		}
	}
	if len(found) > 0 {
		return found, nil
	}
	for _, key := range sandboxEnvAllow[profile] {
		if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") {
			if os.Getenv(key) != "" {
				return nil, nil
			}
		}
	}
	switch profile {
	case "claude":
		return nil, fmt.Errorf("claude keeps no token ghafk can pass into the sandbox. Run `claude setup-token` once and put `CLAUDE_CODE_OAUTH_TOKEN=<token>` in ~/.ghafk/env (chmod 600), or set ANTHROPIC_API_KEY in your environment")
	case "codex":
		return nil, fmt.Errorf("codex has no login file at ~/.codex/auth.json. Run `codex login` as yourself, or store an API key in your environment")
	case "opencode":
		return nil, fmt.Errorf("opencode has no login file at ~/.local/share/opencode/auth.json. Run the opencode login as yourself, or store an API key in your environment")
	default:
		return nil, fmt.Errorf("%s has no login file at ~/%s. Run its login as yourself once, or store an API key in your environment", profile, rel[0])
	}
}

var codexSandboxFlag = regexp.MustCompile(`(^|\s)(?:--sandbox[= ]\S+|-s \S+|--full-auto)`)

func sandboxCommand(command, profile string) string {
	switch profile {
	case "codex":
		command = codexSandboxFlag.ReplaceAllString(command, "${1}--sandbox danger-full-access")
	case "claude":
		if !strings.Contains(command, "--settings") {
			command += ` --settings '{"sandbox":{"enabled":false}}'`
		}
	}
	return command
}

var shellBuiltins = map[string]bool{
	".": true, ":": true, "[": true, "[[": true, "break": true, "case": true,
	"cd": true, "command": true, "continue": true, "do": true, "done": true,
	"echo": true, "elif": true, "else": true, "esac": true, "eval": true,
	"exec": true, "exit": true, "export": true, "fi": true, "for": true,
	"hash": true, "if": true, "in": true, "local": true, "printf": true,
	"read": true, "readonly": true, "return": true, "set": true, "shift": true,
	"source": true, "test": true, "then": true, "trap": true, "true": true,
	"false": true, "type": true, "unset": true, "until": true, "wait": true,
	"while": true,
}

func commandWords(command string) []string {
	var words []string
	for _, segment := range strings.FieldsFunc(stripQuoted(command), func(r rune) bool {
		return r == '\n' || r == ';' || r == '&' || r == '|' || r == '(' || r == ')' || r == '`'
	}) {
		word := ""
		for _, field := range strings.Fields(segment) {
			if looksLikeAssignment(field) {
				continue
			}
			word = strings.Trim(field, "\"'")
			break
		}
		if word == "" || shellBuiltins[word] || strings.ContainsAny(word, "/<>$*?[]{}`\"'") || strings.HasPrefix(word, "-") || !hasLetter(word) {
			continue
		}
		words = append(words, word)
	}
	return words
}

func stripQuoted(command string) string {
	var b strings.Builder
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch c {
		case '\'':
			for i++; i < len(command) && command[i] != '\''; i++ {
			}
		case '"':
			if i+2 < len(command) && command[i+1] == '$' && command[i+2] == '(' {
				b.WriteString("$(")
				i += copySubstitution(&b, command, i+2) - 1
				continue
			}
			for i++; i < len(command) && command[i] != '"'; i++ {
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func copySubstitution(b *strings.Builder, command string, i int) int {
	depth := 1
	for ; i < len(command) && depth > 0; i++ {
		switch command[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				b.WriteByte(')')
				return i + 1
			}
		}
		if depth > 0 {
			b.WriteByte(command[i])
		}
	}
	return i
}

func looksLikeAssignment(field string) bool {
	name, _, ok := strings.Cut(field, "=")
	return ok && isShellName(name)
}

func isShellName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func hasLetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func firstWord(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "\"'")
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = strconv.Quote(item)
	}
	return strings.Join(quoted, ", ")
}

func dedup(items []string) []string {
	seen := map[string]bool{}
	var kept []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			kept = append(kept, item)
		}
	}
	return kept
}

func sandboxEnv(spec sandboxSpec, home, proxyURL string) []string {
	env := sandboxFixedEnv(spec, home, proxyURL)
	extra := spec.passEnv
	for _, kv := range runEnv() {
		name, value, _ := strings.Cut(kv, "=")
		if value == "" {
			continue
		}
		if sandboxEnvKey(name, spec.profile, extra) {
			env = append(env, kv)
			if !containsFold(sandboxCommonEnv, name) && !strings.HasSuffix(name, "_BASE_URL") {
				registerSecret(value)
			}
		}
	}
	return env
}

func sandboxFixedEnv(spec sandboxSpec, home, proxyURL string) []string {
	tmp := filepath.Join(home, "tmp")
	env := []string{
		"HOME=" + home,
		"PATH=" + spec.path,
		"GHAFK_RUN_ID=" + spec.runID,
		"TMPDIR=" + tmp,
		"CLAUDE_CODE_TMPDIR=" + tmp,
		"HTTPS_PROXY=" + proxyURL,
		"HTTP_PROXY=" + proxyURL,
		"ALL_PROXY=" + proxyURL,
		"NO_PROXY=localhost,127.0.0.1,::1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"GOCACHE=" + filepath.Join(spec.cacheDir, "go-build"),
		"GOMODCACHE=" + filepath.Join(spec.cacheDir, "go-mod"),
		"npm_config_cache=" + filepath.Join(spec.cacheDir, "npm"),
		"npm_config_store_dir=" + filepath.Join(spec.cacheDir, "pnpm"),
		"pnpm_config_store_dir=" + filepath.Join(spec.cacheDir, "pnpm"),
		"pnpm_config_cache_dir=" + filepath.Join(spec.cacheDir, "pnpm-cache"),
		"GOPATH=" + filepath.Join(home, "go"),
		"CARGO_HOME=" + filepath.Join(home, ".cargo"),
		"PIP_CACHE_DIR=" + filepath.Join(home, ".cache", "pip"),
		"UV_CACHE_DIR=" + filepath.Join(home, ".cache", "uv"),
	}
	if user := os.Getenv("USER"); user != "" {
		env = append(env, "USER="+user)
	}
	return env
}

var loaderEnvNames = []string{"GCONV_PATH", "GETCONF_DIR", "HOSTALIASES", "LOCALDOMAIN", "LOCPATH", "MALLOC_TRACE", "NIS_PATH", "NLSPATH", "RESOLV_HOST_CONF", "RES_OPTIONS", "TZDIR"}

func loaderEnvName(key string) bool {
	return strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") || containsFold(loaderEnvNames, key)
}

func reservedEnvName(key string) bool {
	if strings.HasPrefix(key, "npm_config_") || strings.HasPrefix(key, "pnpm_config_") || loaderEnvName(key) {
		return true
	}
	for _, kv := range sandboxFixedEnv(sandboxSpec{path: "/usr/bin"}, "/h", "http://127.0.0.1:1") {
		if name, _, _ := strings.Cut(kv, "="); name == key {
			return true
		}
	}
	return key == "GHAFK_RUN_ID" || slices.Contains(ghTokenEnvNames, key)
}

func sandboxEnvKey(name, profile string, extra []string) bool {
	if loaderEnvName(name) {
		return false
	}
	if slices.Contains(ghTokenEnvNames, name) {
		return false
	}
	for _, key := range sandboxCommonEnv {
		if name == key {
			return true
		}
	}
	if keys, ok := sandboxEnvAllow[profile]; ok {
		for _, key := range keys {
			if name == key {
				return true
			}
		}
	}
	for _, key := range extra {
		if name == key {
			return true
		}
	}
	return false
}

func seedIntoRunHome(real, home, runHome string) (loginSeed, bool) {
	rel, err := filepath.Rel(home, real)
	if err != nil {
		return loginSeed{}, false
	}
	run := filepath.Join(runHome, filepath.FromSlash(rel))
	info, err := os.Stat(real)
	if err != nil {
		return loginSeed{}, false
	}
	if err := os.MkdirAll(filepath.Dir(run), 0o700); err != nil {
		return loginSeed{}, false
	}
	if info.IsDir() {
		if copySeedTree(real, run) != nil {
			return loginSeed{}, false
		}
		return loginSeed{real: real, run: run}, true
	}
	if !info.Mode().IsRegular() {
		return loginSeed{}, false
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return loginSeed{}, false
	}
	if err := os.WriteFile(run, data, info.Mode().Perm()); err != nil {
		return loginSeed{}, false
	}
	return loginSeed{real: real, run: run, runResolved: evalPath(run), orig: data}, true
}

func copySeedTree(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() || info.Size() > loginSizeLimit {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

var sandboxOnce sync.Once
var sandboxProbeErr error

func sandboxReady() error {
	sandboxOnce.Do(func() { sandboxProbeErr = probeSandbox() })
	return sandboxProbeErr
}

func runWork(home, repo, issueNum string) string {
	return filepath.Join(workDir(home, repo, issueNum), runSuffix())
}

type seatbeltSpec struct {
	SystemReads []string
	Reads       []string
	DataReads   []string
	Writes      []string
	Execs       []string
	LocalPorts  []int
	Links       []string
	Home        string
}

func pnpmLockDir() string {
	return "/private/tmp/pnpm-store-operation-locks-" + strconv.Itoa(os.Getuid())
}

var seatbeltSysctls = []string{
	"kern.bootargs", "kern.hostname", "kern.iossupportversion", "machdep.cpu.brand_string",
	"kern.maxfiles", "kern.maxfilesperproc", "kern.maxproc",
	"kern.maxvnodes", "kern.ngroups", "kern.osproductversion",
	"kern.osrelease", "kern.ostype", "kern.osvariant_status",
	"kern.osversion", "kern.proc.pid", "kern.version",
	"security.mac.lockdown_mode_state", "vm.malloc_zone_count",
}

func localPortRules(ports []int) string {
	var rules string
	for _, port := range ports {
		rules += " (remote tcp \"localhost:" + strconv.Itoa(port) + "\")"
	}
	return rules
}

func seatbeltProfile(s seatbeltSpec, port string) string {
	subpaths := func(kind string, dirs []string) string {
		var parts []string
		for _, dir := range dedup(dirs) {
			if dir != "" {
				parts = append(parts, "(subpath "+strconv.Quote(dir)+")")
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return "(allow " + kind + " " + strings.Join(parts, " ") + ")"
	}
	writes := append(append([]string{}, s.Writes...), pnpmLockDir())
	ancestorParts := []string{}
	for _, dir := range dedup(append(append(append(append([]string{}, writes...), s.Reads...), s.DataReads...), s.Links...)) {
		ancestorParts = append(ancestorParts, "(path-ancestors "+strconv.Quote(dir)+")")
	}
	readDirs := append(append(append([]string{}, s.Reads...), s.DataReads...), writes...)
	var sysctlParts []string
	for _, name := range seatbeltSysctls {
		sysctlParts = append(sysctlParts, "(sysctl-name "+strconv.Quote(name)+")")
	}
	rules := []string{
		"(version 1)",
		"(deny default)",
		"(allow file-read-data (literal \"/dev/null\") (literal \"/dev/random\") (literal \"/dev/urandom\") (literal \"/dev/autofs_nowait\"))",
		"(allow file-write-data (literal \"/dev/null\") (literal \"/dev/autofs_nowait\"))",
		"(allow file-ioctl (literal \"/dev/autofs_nowait\"))",
		"(allow file-read-metadata (literal \"/var\") (literal \"/tmp\") (literal \"/dev\") (literal \"/dev/fd\") (literal \"/dev/null\"))",
		"(allow file-read* (literal \"/\"))",
		"(allow file-read-data (literal " + strconv.Quote(filepath.Join(s.Home, ".CFUserTextEncoding")) + "))",
		"(allow user-preference-read (preference-domain \"kCFPreferencesAnyApplication\"))",
		"(allow file-write-data (literal \"/dev/tty\") (literal \"/dev/ptmx\"))",
		"(allow file-ioctl (literal \"/dev/ptmx\"))",
		"(allow file-map-executable)",
		subpaths("file-read*", s.SystemReads),
		"(allow file-read-metadata " + strings.Join(ancestorParts, " ") + ")",
		subpaths("file-read*", readDirs),
		subpaths("file-write*", writes),
		subpaths("process-exec", append(append(append([]string{}, s.Execs...), s.Reads...), writes...)),
		"(allow process-exec (literal \"/bin/sh\") (literal \"/usr/bin/env\"))",
		"(allow process-fork)",
		"(allow network-outbound (remote tcp \"localhost:" + port + "\")" + localPortRules(s.LocalPorts) + ")",
		"(allow sysctl-read (sysctl-name-prefix \"hw.\") " + strings.Join(sysctlParts, " ") + ")",
		"(allow signal (target same-sandbox))",
		"(allow process-info* (target same-sandbox))",
		"(allow ipc-posix-shm-read* (ipc-posix-name \"apple.shm.notification_center\") (ipc-posix-name-prefix \"apple.cfprefs.\"))",
		"(allow ipc-posix-shm-write-data (ipc-posix-name \"apple.shm.notification_center\"))",
		"(allow mach-lookup (global-name \"com.apple.system.logger\") (global-name \"com.apple.logd\") (global-name \"com.apple.system.notification_center\") (global-name \"com.apple.cfprefsd.daemon\") (global-name \"com.apple.cfprefsd.agent\") (global-name \"com.apple.system.opendirectoryd.libinfo\") (global-name \"com.apple.trustd.agent\"))",
		"(deny mach-lookup (global-name \"com.apple.securityd\") (global-name \"com.apple.SecurityServer\"))",
	}
	var kept []string
	for _, rule := range rules {
		if rule != "" {
			kept = append(kept, rule)
		}
	}
	return strings.Join(kept, "\n") + "\n"
}

var xcodeSelectLink = "/private/var/db/xcode_select_link"

func developerDirs() (execs, reads []string) {
	execs = []string{"/Library/Developer/CommandLineTools"}
	reads = append([]string{xcodeSelectLink}, execs...)
	if dev := evalPath(xcodeSelectLink); dev != xcodeSelectLink {
		execs = append(execs, dev)
		reads = append(reads, dev)
		if filepath.Base(filepath.Dir(dev)) == "Contents" {
			reads = append(reads, filepath.Dir(dev))
		}
	}
	return execs, reads
}

func seatbeltPaths(spec sandboxSpec) seatbeltSpec {
	s := seatbeltSpec{
		Home:        evalPath(spec.personHome),
		SystemReads: []string{"/bin", "/sbin", "/usr", "/etc", "/private/etc", "/private/var/db/timezone", "/private/var/select", "/Library", "/System"},
		Reads:       []string{},
		Writes:      []string{evalPath(spec.workDir), evalPath(spec.runHome), evalPath(spec.runDir), evalPath(spec.cacheDir)},
		Execs:       []string{"/bin", "/sbin", "/usr"},
	}
	devExecs, devReads := developerDirs()
	s.Execs = append(s.Execs, devExecs...)
	s.Reads = append(s.Reads, devReads...)
	s.LocalPorts = spec.localPorts
	if spec.repoGit != "" {
		s.DataReads = append(s.DataReads, evalPath(spec.repoGit))
		if r := evalPath(spec.repoGit); r != spec.repoGit {
			s.Links = append(s.Links, spec.repoGit)
		}
	}
	for _, dir := range append(append([]string{}, spec.programDirs...), spec.extraBinds...) {
		r := evalPath(dir)
		s.Reads = append(s.Reads, r)
		s.Execs = append(s.Execs, r)
		if r != dir {
			s.Links = append(s.Links, dir)
		}
	}
	return s
}
