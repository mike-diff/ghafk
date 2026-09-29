package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitSetup(t *testing.T) (repo, work, marker string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Cleanup(func() { worktreeGitDirs = map[string]string{} })
	repo = filepath.Join(t.TempDir(), "repo")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		if _, err := run(t.TempDir(), "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	work = workDir(home, repo, "7")
	if err := addWorktree(repo, work, "-b", "agent/7"); err != nil {
		t.Fatal(err)
	}
	return repo, work, filepath.Join(t.TempDir(), "fired")
}

func TestGitIgnoresARedirectedDotGitFile(t *testing.T) {
	_, work, _ := gitSetup(t)
	other := filepath.Join(t.TempDir(), "other")
	if _, err := run(t.TempDir(), "git", "init", "-q", other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: "+filepath.Join(other, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := run(work, "git", "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if got != worktreeGitDirs[work] {
		t.Fatalf("git followed the rewritten .git file to %s, want the recorded %s", got, worktreeGitDirs[work])
	}
}

func TestGitDropsAPlantedWorktreeConfig(t *testing.T) {
	repo, work, _ := gitSetup(t)
	if _, err := run(repo, "git", "config", "extensions.worktreeConfig", "true"); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(worktreeGitDirs[work], "config.worktree")
	if err := os.WriteFile(planted, []byte("[ghafktest]\n\tplanted = yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := run(work, "git", "config", "ghafktest.planted"); got != "" {
		t.Fatalf("git read the planted config.worktree: %q", got)
	}
	if _, err := os.Stat(planted); err == nil {
		t.Fatal("config.worktree is still in the worktree's git dir")
	}
}

func TestGitCommitSkipsRepositoryHooks(t *testing.T) {
	repo, work, marker := gitSetup(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(work, "git", "commit", "-q", "--allow-empty", "-m", "x"); err != nil {
		t.Fatal(err)
	}
	assertNotFired(t, marker, "git commit")
}

func TestGitRefusesAnUnknownWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	dir := filepath.Join(home, ".ghafk", "work", "x", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "status"); err == nil || !strings.Contains(err.Error(), "not a worktree ghafk created") {
		t.Fatalf("git in an unrecorded worktree: err=%v", err)
	}
}

func TestGitStillWorksInARecordedWorktree(t *testing.T) {
	_, work, _ := gitSetup(t)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(work, "git", "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(work, "git", "commit", "-q", "-m", "change"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(work, "git", "log", "-1", "--format=%s"); out != "change" {
		t.Fatalf("commit in worktree: %q", out)
	}
}

func assertNotFired(t *testing.T, marker, step string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("%s ran a repository hook", step)
	}
}
