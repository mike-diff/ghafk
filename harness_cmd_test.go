package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func harnessHome(t *testing.T, harnesses string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ghafk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ghafk", "harnesses"), []byte(harnesses), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	return home
}

func harnessScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHarnessListSortsAndShowsDefault(t *testing.T) {
	script := harnessScript(t, "true\n")
	harnessHome(t, "zz: text "+script+"\naa: text "+script+"\n")
	out := captureStdout(t, func() {
		if err := harnessList(); err != nil {
			t.Error(err)
		}
	})
	if i, j := strings.Index(out, "aa  text"), strings.Index(out, "zz  text"); i < 0 || j < 0 || i > j {
		t.Fatalf("profiles must print sorted by name:\n%s", out)
	}
	if !strings.Contains(out, "aa  text  "+script+"  on PATH\n") {
		t.Fatalf("the script profile must report on PATH:\n%s", out)
	}
	if !strings.Contains(out, "default: (none)\n") {
		t.Fatalf("no default must print (none):\n%s", out)
	}
}

func TestHarnessTestAllChecksPass(t *testing.T) {
	needSandbox(t)
	script := harnessScript(t, "printf after > note.txt\necho ghafk-shell-ok > shell.txt\necho DONE\n")
	home := harnessHome(t, "text: text "+script+"\n")
	if err := os.WriteFile(filepath.Join(home, ".ghafk", "config"), []byte("default: text m1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureStdout(t, func() { err = harnessTest("text", "") })
	if err != nil {
		t.Fatalf("harness test: %v", err)
	}
	for _, line := range []string{
		"exit: ok\n",
		"note.txt: ok\n",
		"shell.txt: ok\n",
		"reply: ok\n",
		"usage: unknown\n",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("output missing %q:\n%s", line, out)
		}
	}
	if !strings.HasPrefix(out, "program: ok (") {
		t.Fatalf("output missing the program pass:\n%s", out)
	}
}

func TestHarnessTestShellCheckFails(t *testing.T) {
	needSandbox(t)
	script := harnessScript(t, "printf after > note.txt\necho DONE\n")
	harnessHome(t, "text: text "+script+"\n")
	var err error
	out := captureStdout(t, func() { err = harnessTest("text", "m1") })
	if err == nil {
		t.Fatal("harness test must fail when a check fails")
	}
	if !strings.Contains(out, "shell.txt: FAIL: missing\n") {
		t.Fatalf("output missing the shell.txt failure:\n%s", out)
	}
	if !strings.Contains(out, "note.txt: ok\n") {
		t.Fatalf("output missing the note.txt pass:\n%s", out)
	}
}

func TestHarnessDefaultKeepsOtherLines(t *testing.T) {
	home := harnessHome(t, "")
	config := filepath.Join(home, ".ghafk", "config")
	if err := os.WriteFile(config, []byte("budget: 5\ndefault: pi old-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := harnessDefault("claude", "opus"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "budget: 5\n") {
		t.Fatalf("the other line must survive:\n%s", text)
	}
	if strings.Count(text, "default:") != 1 || !strings.Contains(text, "default: claude opus\n") {
		t.Fatalf("the default line must be replaced:\n%s", text)
	}
	info, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestHarnessDefaultRefusesBadModel(t *testing.T) {
	home := harnessHome(t, "")
	if err := harnessDefault("claude", "opus; x"); err == nil {
		t.Fatal("a model with shell metacharacters must be refused")
	}
	if err := harnessDefault("nosuch", "opus"); err == nil {
		t.Fatal("an unknown profile must be refused")
	}
	if _, err := os.Stat(filepath.Join(home, ".ghafk", "config")); !os.IsNotExist(err) {
		t.Fatalf("a refused default must not write the config: %v", err)
	}
}
