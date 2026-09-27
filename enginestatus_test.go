package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestATickThatFailsEarlyStillWritesItsStatus(t *testing.T) {
	useOwnerToken(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "env"), []byte("GH_TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnv, cfg)
	if err := tick(); err == nil {
		t.Fatal("a readable env file did not stop the tick")
	}
	data, err := os.ReadFile(statusFile(home))
	if err != nil {
		t.Fatalf("no status file after a failed tick, so the person cannot see why the engine stopped: %v", err)
	}
	var st engineStatus
	if err := json.Unmarshal(data, &st); err != nil || !strings.Contains(st.Error, "chmod 600") || st.Time == "" {
		t.Fatalf("status = %+v, %v; want the error and the time", st, err)
	}
}

func TestTickLogKeepsOutputAndRotates(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".ghafk", "tick.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxTickLog+1), 0o640); err != nil {
		t.Fatal(err)
	}
	stop, err := startTickLog(home)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("shift: no open agent issues")
	fmt.Fprintln(os.Stderr, "shift: checks failed")
	stop()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shift: no open agent issues", "shift: checks failed"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("tick log lacks %q:\n%s", want, data)
		}
	}
	if len(data) > maxTickLog {
		t.Errorf("the log was not rotated at %d bytes, so it grows without limit", maxTickLog)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("the previous log was not kept: %v", err)
	}
}
