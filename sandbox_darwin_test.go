//go:build darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTheGeneratedProfileRunsUnderSandboxExec(t *testing.T) {
	if _, err := os.Stat(sandboxTool); err != nil {
		t.Skipf("sandbox-exec is not available: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	spec := sandboxSpec{
		personHome:  home,
		homeHide:    filepath.Dir(home),
		workDir:     t.TempDir(),
		runDir:      runDir,
		runHome:     filepath.Join(runDir, "home"),
		sockDir:     t.TempDir(),
		cacheDir:    t.TempDir(),
		programDirs: []string{"/usr/bin"},
		runID:       "profiletest",
	}
	profile := seatbeltProfile(seatbeltPaths(spec), "3199")
	cmd := exec.Command(sandboxTool, "-p", profile, "--", "/usr/bin/true")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("sandbox-exec rejected the generated profile: %v\noutput:\n%s\nprofile:\n%s", err, out.String(), profile)
	}
	if msg := out.String(); msg != "" {
		t.Fatalf("sandbox-exec reported warnings for the profile: %s\nprofile:\n%s", msg, profile)
	}
}
