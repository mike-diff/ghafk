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
		}
	}
	return s, nil
}
