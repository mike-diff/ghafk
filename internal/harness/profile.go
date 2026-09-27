// Package harness defines the coding harness profiles ghafk can run and reads their output.
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Profile is a harness command template with a {model} placeholder and the parser for its output.
type Profile struct {
	Parser  string
	Command string
	Allow   []string
}

type Env struct {
	Profiles map[string]Profile
	Default  string
}

// Role is the resolved command a repository runs for one step.
type Role struct {
	Command string
	Parser  string
	Label   string
}

var ValidModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)

// Builtins returns the profiles ghafk ships with.
func Builtins() map[string]Profile {
	return map[string]Profile{
		"pi":       {Parser: "pi-json", Command: "pi -p --no-session --mode json --model {model}"},
		"omp":      {Parser: "pi-json", Command: "omp -p --mode json --model {model}"},
		"claude":   {Parser: "claude-json", Command: "claude -p --output-format json --model {model} --permission-mode acceptEdits --allowedTools Bash"},
		"codex":    {Parser: "codex-json", Command: "codex exec --json --skip-git-repo-check --sandbox workspace-write --model {model} -"},
		"sesh":     {Parser: "text", Command: `sesh -yes -model {model} -p "$(cat)"`},
		"opencode": {Parser: "opencode-json", Command: "opencode run --format json -m {model}"},
	}
}

// LoadEnv returns the built-in profiles overlaid with dir/harnesses and the default from dir/config.
// Missing files are skipped.
func LoadEnv(dir string) (Env, error) {
	henv := Env{Profiles: Builtins()}
	henv, err := applyHarnessFile(henv, filepath.Join(dir, "harnesses"))
	if err != nil {
		return Env{}, err
	}
	return applyConfigFile(henv, filepath.Join(dir, "config"))
}

func applyHarnessFile(henv Env, path string) (Env, error) {
	data, err := readIfExists(path)
	if err != nil {
		return Env{}, err
	}
	for i, line := range strings.Split(data, "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if base, isAllow := strings.CutSuffix(name, ".allow"); isAllow {
			if !ok || base == "" || strings.ContainsAny(base, " \t") {
				return Env{}, fmt.Errorf("%s:%d: bad harness line %q", path, i+1, line)
			}
			p := henv.Profiles[base]
			p.Allow = strings.Fields(strings.TrimSpace(rest))
			henv.Profiles[base] = p
			continue
		}
		parserName, cmd, _ := strings.Cut(strings.TrimSpace(rest), " ")
		cmd = strings.TrimSpace(cmd)
		_, known := parsers[parserName]
		if !ok || name == "" || strings.ContainsAny(name, " \t") || !known || cmd == "" {
			return Env{}, fmt.Errorf("%s:%d: bad harness line %q", path, i+1, line)
		}
		p := henv.Profiles[name]
		henv.Profiles[name] = Profile{Parser: parserName, Command: cmd, Allow: p.Allow}
	}
	return henv, nil
}

func applyConfigFile(henv Env, path string) (Env, error) {
	data, err := readIfExists(path)
	if err != nil {
		return Env{}, err
	}
	for _, line := range strings.Split(data, "\n") {
		key, value, _ := strings.Cut(line, ":")
		if strings.TrimSpace(key) == "default" {
			henv.Default = strings.TrimSpace(value)
		}
	}
	return henv, nil
}

// ResolveRole turns "<profile> <model>" into a command. Any other value is kept as a raw shell command.
func ResolveRole(value string, profiles map[string]Profile) (Role, error) {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	if len(fields) > 0 {
		if p, ok := profiles[fields[0]]; ok {
			model := strings.TrimSpace(value[len(fields[0]):])
			if !ValidModel.MatchString(model) {
				return Role{}, fmt.Errorf("bad model %q for harness %q", model, fields[0])
			}
			return Role{
				Command: strings.ReplaceAll(p.Command, "{model}", model),
				Parser:  p.Parser,
				Label:   fields[0] + " " + model,
			}, nil
		}
	}
	return Role{Command: value, Label: "command"}, nil
}

func readIfExists(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}
