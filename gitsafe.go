package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var worktreeGitDirs = map[string]string{}

var gitHardening = []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=", "-c", "protocol.ext.allow=never"}

func addWorktree(repo, work string, rest ...string) error {
	if _, err := run(repo, "git", append([]string{"worktree", "add", work}, rest...)...); err != nil {
		return err
	}
	gitDir, err := worktreeAdminDir(repo, work)
	if err != nil {
		return err
	}
	worktreeGitDirs[work] = gitDir
	return nil
}

func worktreeAdminDir(repo, work string) (string, error) {
	common, err := run(repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	want, err := filepath.EvalSymlinks(filepath.Join(work, ".git"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		dir := filepath.Join(common, "worktrees", e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "gitdir"))
		if err != nil {
			continue
		}
		if got, err := filepath.EvalSymlinks(strings.TrimSpace(string(data))); err == nil && got == want {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no git admin dir for worktree %s", work)
}

func hardenGit(dir string, args []string) ([]string, error) {
	pre := append([]string{}, gitHardening...)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(filepath.Join(home, ".ghafk", "work"), dir)
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		gitDir, ok := worktreeGitDirs[dir]
		if !ok {
			return nil, fmt.Errorf("git in %s: not a worktree ghafk created in this run", dir)
		}
		if err := os.Remove(filepath.Join(gitDir, "config.worktree")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		pre = append(pre, "--git-dir="+gitDir, "--work-tree="+dir)
	}
	return append(pre, args...), nil
}
