package main

import (
	"os"
	"path/filepath"
)

const configDirEnv = "GHAFK_CONFIG_DIR"

func configDir() (string, error) {
	if dir := os.Getenv(configDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ghafk"), nil
}
