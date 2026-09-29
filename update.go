package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const modulePath = "github.com/mike-diff/ghafk"

func update() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("ghafk update needs Go 1.26 or newer on your PATH")
	}
	fmt.Println("ghafk: installing the newest ghafk from main")
	install := exec.Command("go", "install", modulePath+"@main")
	install.Env = append(os.Environ(), "GOPROXY=direct")
	install.Stdout, install.Stderr = os.Stdout, os.Stderr
	if err := install.Run(); err != nil {
		return err
	}
	bin, err := installedBinary()
	if err != nil {
		return err
	}
	fmt.Println("ghafk: installed " + bin)
	return nil
}

func installedBinary() (string, error) {
	out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		return filepath.Join(strings.TrimSpace(lines[0]), "ghafk"), nil
	}
	if len(lines) < 2 {
		return "", fmt.Errorf("go env printed no GOPATH")
	}
	return filepath.Join(strings.Split(strings.TrimSpace(lines[1]), string(os.PathListSeparator))[0], "bin", "ghafk"), nil
}
