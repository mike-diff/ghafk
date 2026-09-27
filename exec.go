package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func discardWork(repo, work, branch string) {
	worktreeRemove(repo, work)
	_, _ = run(repo, "git", "branch", "-D", branch)
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func runEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
		default:
			env = append(env, kv)
		}
	}
	return env
}

func workerChanged(status, head, before string) bool {
	return status != "" || head != before
}

func clearStale(repo, work, branch string) error {
	worktreeRemove(repo, work)
	run(repo, "git", "worktree", "prune")
	if listed, err := run(repo, "git", "branch", "--list", branch); err == nil && listed != "" {
		_, err := run(repo, "git", "branch", "-D", branch)
		return err
	}
	return nil
}

func worktreeRemove(repo, work string) {
	delete(worktreeGitDirs, work)
	if _, err := run(repo, "git", "worktree", "remove", "--force", work); err != nil {
		os.RemoveAll(work)
	}
}

var (
	pipeGrace   = 10 * time.Second
	toolTimeout = 10 * time.Minute
)

func runShell(name, dir, command, stdin string, timeout time.Duration, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = runEnv()
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = pipeGrace
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return nil
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func run(dir, name string, args ...string) (string, error) {
	return runWithEnv(dir, name, nil, args...)
}

func runWithEnv(dir, name string, env []string, args ...string) (string, error) {
	stdin := ""
	switch name {
	case "gh":
		args, stdin = bodyToStdin(args)
	case "git":
		hardened, err := hardenGit(dir, args)
		if err != nil {
			return "", err
		}
		args = hardened
		if env == nil {
			env = withOwnerToken(runEnv())
		}
		env = append(append([]string{}, env...), "GIT_CONFIG_NOSYSTEM=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = pipeGrace
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimLeft(strings.TrimRight(stdout.String(), " \t\r\n"), "\r\n")
	if err != nil {
		err = fmt.Errorf("%s %s: %w", name, clipArgs(args), err)
		if detail := firstLine(stderr.String()); detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
	}
	return out, err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func bodyToStdin(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	stdin := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--body" && i+1 < len(args):
			out = append(out, "--body-file", "-")
			stdin = args[i+1]
			i++
		case args[i] == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "body="):
			out = append(out, "-F", "body=@-")
			stdin = strings.TrimPrefix(args[i+1], "body=")
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out, stdin
}

func clipArgs(args []string) string {
	clipped := make([]string, len(args))
	for i, a := range args {
		if len(a) > 60 {
			a = a[:60] + "..."
		}
		clipped[i] = a
	}
	s := strings.Join(clipped, " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func workDir(home, repo, issueNum string) string {
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(home, ".ghafk", "work", filepath.Base(repo)+"-"+hex.EncodeToString(sum[:4]), issueNum)
}
