package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

var ownerToken string

var envKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func loadEngineEnv() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "env")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ghapp.SecretMode(info) {
		return fmt.Errorf("%s holds secrets and must be readable only by its owner: chmod 600 %s", path, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKey.MatchString(key) {
			return fmt.Errorf("%s line %d: want KEY=value", path, n+1)
		}
		if key == "GH_TOKEN" {
			ownerToken = value
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

func withOwnerToken(env []string) []string {
	if ownerToken == "" {
		return env
	}
	return append(append([]string{}, env...), "GH_TOKEN="+ownerToken)
}
