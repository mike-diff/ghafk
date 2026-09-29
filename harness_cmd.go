package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

const harnessTestTimeout = 5 * time.Minute

const harnessTestPrompt = "Change note.txt so it holds the single word after. Then run the shell command `echo ghafk-shell-ok > shell.txt`. Then reply with the single word DONE.\n"

func runHarness(args []string) error {
	if len(args) == 0 {
		usage()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		if len(rest) != 0 {
			usage()
		}
		return harnessList()
	case "default":
		if len(rest) != 2 {
			usage()
		}
		return harnessDefault(rest[0], rest[1])
	case "test":
		if len(rest) != 1 && len(rest) != 2 {
			usage()
		}
		model := ""
		if len(rest) == 2 {
			model = rest[1]
		}
		if err := loadEngineEnv(); err != nil {
			return err
		}
		return harnessTest(rest[0], model)
	}
	usage()
	return nil
}

func harnessList() error {
	henv, err := loadHarnessEnv()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(henv.Profiles))
	for name := range henv.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := henv.Profiles[name]
		state := "not on PATH"
		if harnessOnPath(p.Command) {
			state = "on PATH"
		}
		fmt.Printf("%s  %s  %s  %s\n", name, p.Parser, p.Command, state)
	}
	def := henv.Default
	if def == "" {
		def = "(none)"
	}
	fmt.Println("default: " + def)
	return nil
}

func harnessOnPath(command string) bool {
	_, err := harnessLookPath(command)
	return err == nil
}

func harnessLookPath(command string) (string, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}
	return exec.LookPath(fields[0])
}

func harnessDefault(profile, model string) error {
	henv, err := loadHarnessEnv()
	if err != nil {
		return err
	}
	if _, err := resolveHarnessRole(profile, model, henv.Profiles); err != nil {
		return err
	}
	config, err := configPath()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("default: %s %s", profile, model)
	data, err := os.ReadFile(config)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if text := strings.TrimSuffix(string(data), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	replaced := false
	for i, l := range lines {
		key, _, _ := strings.Cut(l, ":")
		if strings.TrimSpace(key) == "default" {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(config, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(config, 0o600)
}

func resolveHarnessRole(profile, model string, profiles map[string]harness.Profile) (harness.Role, error) {
	if _, ok := profiles[profile]; !ok {
		return harness.Role{}, fmt.Errorf("unknown harness %q", profile)
	}
	return harness.ResolveRole(strings.TrimSpace(profile+" "+model), profiles)
}

type harnessResult struct {
	line string
	pass bool
}

func harnessOk(name, note string) harnessResult {
	if note == "" {
		return harnessResult{line: name + ": ok", pass: true}
	}
	return harnessResult{line: name + ": ok (" + note + ")", pass: true}
}

func harnessFail(name, reason string) harnessResult {
	return harnessResult{line: name + ": FAIL: " + reason}
}

func harnessFileCheck(dir, name, want string) harnessResult {
	data, err := readSmallRegularFile(filepath.Join(dir, name), 1<<16)
	if err != nil {
		return harnessFail(name, "missing")
	}
	if got := strings.TrimSpace(string(data)); got != want {
		return harnessFail(name, fmt.Sprintf("holds %q", got))
	}
	return harnessOk(name, "")
}

func harnessTest(profile, model string) error {
	henv, err := loadHarnessEnv()
	if err != nil {
		return err
	}
	if _, ok := henv.Profiles[profile]; !ok {
		return fmt.Errorf("unknown harness %q", profile)
	}
	if model == "" {
		def := strings.Fields(henv.Default)
		if len(def) > 0 && def[0] == profile {
			model = strings.TrimSpace(henv.Default[len(def[0]):])
		} else {
			return fmt.Errorf("harness test %s: model argument required", profile)
		}
	}
	r, err := resolveHarnessRole(profile, model, henv.Profiles)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ghafk-harness-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if _, err := run(work, "git", "init"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("before\n"), 0o644); err != nil {
		return err
	}
	var out bytes.Buffer
	runErr := runSandboxed(sandboxOpts{name: "worker", dir: work, command: r.Command, stdin: harnessTestPrompt, timeout: harnessTestTimeout, role: &r}, &out, os.Stderr)

	results := make([]harnessResult, 0, 6)
	if path, err := harnessLookPath(r.Command); err != nil {
		results = append(results, harnessFail("program", err.Error()))
	} else {
		results = append(results, harnessOk("program", path))
	}
	if runErr != nil {
		results = append(results, harnessFail("exit", runErr.Error()))
	} else {
		results = append(results, harnessOk("exit", ""))
	}
	results = append(results, harnessFileCheck(work, "note.txt", "after"))
	results = append(results, harnessFileCheck(work, "shell.txt", "ghafk-shell-ok"))
	reply, u := harness.ParseOutput(r.Parser, out.String())
	if strings.Contains(reply, "DONE") {
		results = append(results, harnessOk("reply", ""))
	} else {
		first := firstLine(reply)
		if len(first) > 60 {
			first = first[:60] + "..."
		}
		results = append(results, harnessFail("reply", fmt.Sprintf("no DONE in %q", first)))
	}
	if u.Known {
		results = append(results, harnessOk("usage", u.String()))
	} else {
		results = append(results, harnessResult{line: "usage: unknown", pass: true})
	}

	failed := 0
	for _, res := range results {
		fmt.Println(res.line)
		if !res.pass {
			failed++
		}
	}
	if failed == 1 {
		return fmt.Errorf("harness test %s: 1 check failed", r.Label)
	}
	if failed > 1 {
		return fmt.Errorf("harness test %s: %d checks failed", r.Label, failed)
	}
	return nil
}
