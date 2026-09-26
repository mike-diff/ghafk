package main

import (
	"os"
	"path/filepath"
)

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func orError(out string, err error) string {
	if out == "" && err != nil {
		return "unavailable: " + err.Error()
	}
	return out
}
