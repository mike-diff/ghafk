package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const modulePath = "github.com/mike-diff/ghafk"

func update() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("ghafk update needs Go 1.26 or newer on your PATH")
	}
	version, env := "main", append(os.Environ(), "GOPROXY=direct")
	tags, err := exec.Command("git", "ls-remote", "--tags", "--refs", "https://"+modulePath).Output()
	if err != nil {
		return fmt.Errorf("ghafk update could not list the releases: %w", err)
	}
	if tag := newestRelease(string(tags)); tag != "" {
		version, env = tag, os.Environ()
	}
	fmt.Println("ghafk: installing ghafk " + version)
	install := exec.Command("go", "install", modulePath+"@"+version)
	install.Env = env
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
	return binaryFromGoEnv(string(out))
}

func binaryFromGoEnv(out string) (string, error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		return filepath.Join(strings.TrimSpace(lines[0]), "ghafk"), nil
	}
	if len(lines) < 2 {
		return "", fmt.Errorf("go env printed no GOPATH")
	}
	return filepath.Join(strings.Split(strings.TrimSpace(lines[1]), string(os.PathListSeparator))[0], "bin", "ghafk"), nil
}

var releaseTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func newestRelease(refs string) string {
	best, bestParts := "", [3]int{-1, -1, -1}
	for _, line := range strings.Split(refs, "\n") {
		_, ref, _ := strings.Cut(line, "\t")
		m := releaseTag.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(ref), "refs/tags/"))
		if m == nil {
			continue
		}
		var parts [3]int
		for i := range parts {
			parts[i], _ = strconv.Atoi(m[i+1])
		}
		if slices.Compare(parts[:], bestParts[:]) > 0 {
			best, bestParts = m[0], parts
		}
	}
	return best
}
