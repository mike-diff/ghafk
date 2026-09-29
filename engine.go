package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type engineLayout struct {
	user, home, bin, etc string
}

func (l engineLayout) config() string { return filepath.Join(l.home, ".ghafk") }

var runPrivileged = func(stdin string, args ...string) (string, error) {
	cmd := exec.Command("sudo", append([]string{"-n"}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		return out, fmt.Errorf("sudo %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

var sudoValidate = func() error {
	cmd := exec.Command("sudo", "-v")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

var readSecret = func(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("a terminal is needed to enter the token: %v", err)
	}
	defer tty.Close()
	stty := func(arg string) {
		cmd := exec.Command("stty", arg)
		cmd.Stdin = tty
		cmd.Run()
	}
	fmt.Fprint(tty, prompt)
	stty("-echo")
	defer stty("echo")
	line, err := bufio.NewReader(tty).ReadString('\n')
	fmt.Fprintln(tty)
	return strings.TrimSpace(line), err
}

func installRoot(content, dest, group string, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "ghafk-engine-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_, err = runPrivileged("", "install", "-o", "root", "-g", group, "-m", fmt.Sprintf("%04o", mode), tmp.Name(), dest)
	return err
}

func writeAsEngine(l engineLayout, content string, mode os.FileMode, dest string) error {
	_, err := runPrivileged(content, "-u", l.user, l.bin, "engine", "write", dest, fmt.Sprintf("%04o", mode))
	return err
}

func readAsEngine(l engineLayout, path string) (string, error) {
	return runPrivileged("", "-u", l.user, "cat", path)
}

func engineWrite(args []string) error {
	if len(args) != 2 {
		usage()
	}
	dest := args[0]
	mode, err := strconv.ParseUint(args[1], 8, 32)
	if err != nil || mode&^0o777 != 0 {
		return fmt.Errorf("mode %q must be octal permission bits", args[1])
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ghafk-write-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(os.FileMode(mode)); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

func readEngineFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

func tokenURL() string {
	q := url.Values{}
	q.Set("name", "ghafk engine")
	q.Set("description", "ghafk engine account: opens and merges pull requests")
	q.Set("expires_in", "366")
	q.Set("contents", "write")
	q.Set("issues", "write")
	q.Set("pull_requests", "write")
	return "https://github.com/settings/personal-access-tokens/new?" + q.Encode()
}

func renderEngineEnv(existing, token string) string {
	var kept []string
	for _, line := range strings.Split(existing, "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.TrimSpace(line) == "" || key == "GH_TOKEN" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(append([]string{"GH_TOKEN=" + token}, kept...), "\n") + "\n"
}

func renderEngineGitconfig(name, email, ghPath string) string {
	return "[user]\n\tname = " + name + "\n\temail = " + email + "\n" +
		"[credential \"https://github.com\"]\n\thelper =\n\thelper = !" + ghPath + " auth git-credential\n"
}

func verifyToken(token string) (string, time.Time, error) {
	out, err := runWithEnv(".", "gh", append(runEnv(), "GH_TOKEN="+token), "api", "--include", "user", "--jq", ".login")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("GitHub refused the token: %v", err)
	}
	login, expires := parseOwner(out)
	return login, expires, nil
}

func askToken(l engineLayout) (string, error) {
	fmt.Println("ghafk: the engine needs a fine-grained GitHub token of the account that must author and merge pull requests.")
	fmt.Println("ghafk: create it here, select the repositories that ghafk works, then paste it below:")
	fmt.Println("  " + tokenURL())
	token, err := readSecret("GitHub token: ")
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("no token entered")
	}
	login, expires, err := verifyToken(token)
	if err != nil {
		return "", err
	}
	fmt.Printf("ghafk: the token belongs to %s; %s\n", login, tokenStatus(expires, time.Now()))
	return token, nil
}

func storeToken(l engineLayout, token string) error {
	existing, _ := runPrivileged("", "cat", filepath.Join(l.etc, "env"))
	return installRoot(renderEngineEnv(existing, token), filepath.Join(l.etc, "env"), l.user, 0o640)
}

func hasToken(l engineLayout) bool {
	_, err := runPrivileged("", "grep", "-q", "^GH_TOKEN=.", filepath.Join(l.etc, "env"))
	return err == nil
}

func syncConfig(l engineLayout, personHome string) error {
	src := filepath.Join(personHome, ".ghafk")
	for _, dir := range []string{l.etc, filepath.Join(l.etc, "prompts")} {
		if _, err := runPrivileged("", "install", "-d", "-o", "root", "-g", rootGroup, "-m", "0755", dir); err != nil {
			return err
		}
	}
	if err := migrateEngineConfig(l); err != nil {
		return err
	}
	for _, name := range []string{"config", "harnesses", "app", "app.pem"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if os.IsNotExist(err) {
			if name == "config" || name == "harnesses" {
				runPrivileged("", "rm", "-f", filepath.Join(l.etc, name))
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := installRoot(string(data), filepath.Join(l.etc, name), l.user, 0o640); err != nil {
			return err
		}
	}
	prompts, err := filepath.Glob(filepath.Join(src, "prompts", "*.md"))
	if err != nil {
		return err
	}
	if _, err := runPrivileged("", "find", filepath.Join(l.etc, "prompts"), "-mindepth", "1", "-delete"); err != nil {
		return err
	}
	for _, p := range prompts {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := installRoot(string(data), filepath.Join(l.etc, "prompts", filepath.Base(p)), l.user, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func migrateEngineConfig(l engineLayout) error {
	old := l.config()
	if env, err := readAsEngine(l, filepath.Join(old, "env")); err == nil && !hasToken(l) {
		for _, line := range strings.Split(env, "\n") {
			if token, ok := strings.CutPrefix(strings.TrimSpace(line), "GH_TOKEN="); ok && token != "" {
				if err := storeToken(l, token); err != nil {
					return err
				}
			}
		}
	}
	for _, name := range []string{"app", "app.pem"} {
		data, err := readAsEngine(l, filepath.Join(old, name))
		if err != nil || data == "" {
			continue
		}
		if _, err := runPrivileged("", "test", "-e", filepath.Join(l.etc, name)); err == nil {
			continue
		}
		if err := installRoot(data+"\n", filepath.Join(l.etc, name), l.user, 0o640); err != nil {
			return err
		}
	}
	_, err := runPrivileged("", "-u", l.user, "rm", "-rf", filepath.Join(old, "env"), filepath.Join(old, "app"), filepath.Join(old, "app.pem"),
		filepath.Join(old, "config"), filepath.Join(old, "harnesses"), filepath.Join(old, "prompts"))
	return err
}

func personGitIdentity() (string, string, error) {
	name, err := run(".", "git", "config", "--global", "user.name")
	if err != nil || name == "" {
		return "", "", fmt.Errorf("set your git identity first: git config --global user.name and user.email")
	}
	email, err := run(".", "git", "config", "--global", "user.email")
	if err != nil || email == "" {
		return "", "", fmt.Errorf("set your git identity first: git config --global user.email")
	}
	return name, email, nil
}

func systemTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", name)
	}
	if strings.HasPrefix(path, "/home/") || strings.HasPrefix(path, "/Users/") || strings.HasPrefix(path, "/root/") {
		return "", fmt.Errorf("%s is at %s; the engine cannot use a program in a personal home directory, install it system-wide", name, path)
	}
	return path, nil
}

func isEngineAccount(l engineLayout) bool {
	u, err := user.Current()
	return err == nil && u.Username == l.user
}

func refuseBesideEngine(verb string) error {
	l, ok := installedEngine()
	if !ok || isEngineAccount(l) {
		return nil
	}
	return fmt.Errorf("an engine runs as the %s account on this machine; `ghafk %s` here would race it. Run `ghafk engine remove --purge` to move to the sandboxed engine, then `ghafk start`", l.user, verb)
}

func sanitizeTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
			return r
		}
		return -1
	}, s)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func runEngine(args []string) error {
	if len(args) == 0 {
		usage()
	}
	switch args[0] {
	case "setup", "token", "start", "stop":
		fmt.Fprintln(os.Stderr, "ghafk: the engine account is deprecated. Agents now run in a sandbox as you: run `ghafk engine remove --purge`, then `ghafk start`.")
	}
	switch args[0] {
	case "setup":
		return engineSetup()
	case "token":
		return engineReplaceToken()
	case "start":
		return engineStart()
	case "stop":
		return engineStop()
	case "write":
		return engineWrite(args[1:])
	case "remove":
		purge := len(args) == 2 && args[1] == "--purge"
		if len(args) > 2 || len(args) == 2 && !purge {
			usage()
		}
		return engineRemove(purge)
	}
	usage()
	return nil
}

func engineReplaceToken() error {
	l, ok := installedEngine()
	if !ok {
		return fmt.Errorf("no engine is set up; run `ghafk engine setup`")
	}
	token, err := askToken(l)
	if err != nil {
		return err
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	if err := storeToken(l, token); err != nil {
		return err
	}
	fmt.Println("ghafk: the engine uses the new token from its next tick. Delete the old token on GitHub.")
	return nil
}

func engineStatusView(l engineLayout) error {
	if _, err := os.Stat(l.config()); os.IsPermission(err) {
		if reexecWithGroup(l) {
			return nil
		}
		return fmt.Errorf("you cannot read the engine's files yet: log out and in again so that your membership in the %s group applies", l.user)
	}
	fmt.Println(sanitizeTerminal(engineServiceState(l)))
	fmt.Println()
	data, err := readEngineFile(statusFile(l.home), 1<<20)
	if os.IsNotExist(err) {
		fmt.Println("engine: no tick has finished yet")
		return nil
	}
	if err != nil {
		return err
	}
	var st engineStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("%s: %v", statusFile(l.home), err)
	}
	fmt.Println(sanitizeTerminal(renderEngineStatus(st, time.Now())))
	if log, err := readEngineFile(filepath.Join(l.config(), "tick.log"), 2*maxTickLog); err == nil {
		fmt.Println("\n" + sanitizeTerminal(tailLines(string(log), 20)))
	}
	return nil
}

func renderEngineStatus(st engineStatus, now time.Time) string {
	lines := []string{"last tick: " + st.Time}
	if st.Error != "" {
		lines = append(lines, "last tick failed: "+st.Error)
	}
	if st.Owner != "" {
		expires, _ := time.Parse(time.RFC3339, st.TokenExpires)
		lines = append(lines, "owner: "+st.Owner, tokenStatus(expires, now))
		if w := tokenWarning(st.Owner, expires, now); w != "" {
			lines = append(lines, w)
		}
	}
	if st.Account != "" {
		lines = append(lines, "engine account: "+st.Account)
	}
	if len(st.Harnesses) == 0 {
		lines = append(lines, "warning: the engine finds no harness on its PATH")
	} else {
		lines = append(lines, "harnesses: "+strings.Join(st.Harnesses, ", "))
	}
	for _, r := range st.Repos {
		lines = append(lines, r.Name+": "+r.State)
	}
	return strings.Join(lines, "\n")
}

func reportHarnesses(l engineLayout, found []string) {
	if len(found) == 0 {
		fmt.Printf("ghafk: warning: the engine finds no harness. Install one as the engine account (`sudo -iu %s`, then the harness's own install and login), close that shell, and run `ghafk engine setup` again.\n", l.user)
		return
	}
	fmt.Printf("ghafk: harnesses on the engine's PATH: %s\n", strings.Join(found, ", "))
}
