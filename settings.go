package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var allColumns = []string{"step", "status", "started", "ended", "duration", "tokens", "harness"}

var defaultColumns = []string{"step", "status", "duration", "tokens", "harness"}

var progressColumns = defaultColumns

type settings struct {
	progress []string
	interval int
	skip     []string
	repos    []string
	bind     []string
	env      []string
	egress   []string
	local    []int
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config"), nil
}

func loadMachineSettings() (settings, error) {
	path, err := configPath()
	if err != nil {
		return settings{}, err
	}
	return loadSettings(path)
}

func loadSettings(path string) (settings, error) {
	s := settings{progress: defaultColumns, interval: 2}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "progress":
			cols := strings.Fields(strings.ToLower(value))
			for _, c := range cols {
				if !slices.Contains(allColumns, c) {
					return s, fmt.Errorf("config: unknown progress column %q; use %s", c, strings.Join(allColumns, ", "))
				}
			}
			if !slices.Contains(cols, "step") {
				return s, fmt.Errorf("config: progress must include step")
			}
			s.progress = cols
		case "interval":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 60 || 60%n != 0 {
				return s, fmt.Errorf("config: interval %q must be a number of minutes that divides 60, such as 2, 5 or 15", value)
			}
			s.interval = n
		case "skip":
			if strings.Count(value, "/") != 1 {
				return s, fmt.Errorf("config: skip %q must name a repository as owner/name", value)
			}
			s.skip = append(s.skip, value)
		case "repo":
			if strings.Count(value, "/") != 1 || strings.HasPrefix(value, "-") {
				return s, fmt.Errorf("config: repo %q must name a repository as owner/name", value)
			}
			s.repos = append(s.repos, value)
		case "bind":
			for _, dir := range strings.Fields(value) {
				if !filepath.IsAbs(dir) {
					return s, fmt.Errorf("config: bind %q must be an absolute path to a directory", dir)
				}
				if reason := unsafeBind(dir); reason != "" {
					return s, fmt.Errorf("config: bind %q %s; bind only the directory a program lives in", dir, reason)
				}
			}
			s.bind = append(s.bind, strings.Fields(value)...)
		case "env":
			for _, key := range strings.Fields(value) {
				if !isShellName(key) || !hasLetter(key) {
					return s, fmt.Errorf("config: env %q must be an environment variable name such as NPM_TOKEN", key)
				}
				if reservedEnvName(key) {
					return s, fmt.Errorf("config: env %q is set by the sandbox and cannot be passed through", key)
				}
			}
			s.env = append(s.env, strings.Fields(value)...)
		case "local":
			for _, field := range strings.Fields(value) {
				port, err := strconv.Atoi(field)
				if err != nil || port < 1 || port > 65535 {
					return s, fmt.Errorf("config: local %q must be a port number on this machine, such as 11434", field)
				}
				s.local = append(s.local, port)
			}
		case "egress":
			for _, host := range strings.Fields(value) {
				if !validEgressHost(host) {
					return s, fmt.Errorf("config: egress %q must be a host name such as api.example.com or *.example.com", host)
				}
			}
			s.egress = append(s.egress, strings.Fields(value)...)
		}
	}
	return s, nil
}

func unsafeBind(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return exposesSecrets(dir, home)
}

var secretDirs = []string{".ssh", ".gnupg", ".aws", ".ghafk", ".config/gh", ".local/share/keyrings", ".local/share/opencode", ".config/opencode", ".claude", ".codex", ".pi", ".omp", ".sesh"}

func exposesSecrets(dir, home string) string {
	for _, d := range dedup([]string{filepath.Clean(dir), evalPath(dir)}) {
		for _, h := range dedup([]string{home, evalPath(home)}) {
			if d == "/" || underTree(h, d) {
				return "would expose your whole home"
			}
			for _, shared := range []string{"/tmp", "/private/tmp", "/run", "/var/run", "/private/var/run", "/var/tmp", "/private/var/tmp"} {
				if d == shared {
					return "is shared with other programs and their sockets"
				}
			}
			for _, secret := range secretDirs {
				if underTree(filepath.Join(h, secret), d) || underTree(d, filepath.Join(h, secret)) {
					return "holds logins or keys"
				}
			}
		}
	}
	return ""
}

func underTree(path, tree string) bool {
	return strings.HasPrefix(path+string(os.PathSeparator), tree+string(os.PathSeparator))
}
