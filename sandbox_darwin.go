//go:build darwin

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const sandboxTool = "/usr/bin/sandbox-exec"

const sandboxUsernsFix = ""

const preflightProfile = `(version 1)(deny default)(allow process-exec (literal "/usr/bin/true"))(allow file-read*)`

func runSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "r" + hex.EncodeToString(b[:])
}

func runParent(work string) string { return filepath.Dir(work) }

func sharedSandboxCache() bool { return false }

func probeSandbox() error {
	if _, err := os.Stat(sandboxTool); err != nil {
		return fmt.Errorf("the sandbox needs %s, which this macOS no longer ships", sandboxTool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandboxTool, "-p", preflightProfile, "--", "/usr/bin/true")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the sandbox does not start: %.200s", out.String())
	}
	return nil
}

func proxyStart(proxy *egressProxy, spec sandboxSpec) error {
	return proxy.listen("tcp", "127.0.0.1:0")
}

func sandboxLaunch(o sandboxOpts, spec sandboxSpec, proxy *egressProxy, command string, stdout, stderr io.Writer) error {
	_, port, err := net.SplitHostPort(proxy.addr())
	if err != nil {
		return err
	}
	profile := seatbeltProfile(seatbeltPaths(spec), port)
	proxyURL := "http://127.0.0.1:" + port
	env := sandboxEnv(spec, spec.runHome, proxyURL)
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandboxTool, "-p", profile, "--", "/bin/sh", "-c", command)
	cmd.Dir = o.dir
	cmd.Stdin = strings.NewReader(o.stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = pipeGrace
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", o.name, err)
	}
	sess := sessionOf(cmd.Process.Pid)
	runErr := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	sweepRunawayProcesses(spec.runID, sess)
	if runErr != nil {
		return fmt.Errorf("%s: %w", o.name, runErr)
	}
	return nil
}

func sessionOf(pid int) string {
	out, err := exec.Command("ps", "-o", "sess=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sweepRunawayProcesses(runID, sess string) {
	marker := "GHAFK_RUN_ID=" + runID
	self := strconv.Itoa(os.Getpid())
	if out, err := exec.Command("ps", "-axEww", "-o", "pid=,command=").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, marker) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] == self {
				continue
			}
			if pid, err := strconv.Atoi(fields[0]); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	if sess == "" || sess == "0" {
		return
	}
	if out, err := exec.Command("ps", "-axo", "sess=,pid=").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != sess || fields[1] == self {
				continue
			}
			if pid, err := strconv.Atoi(fields[1]); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

func staleRuns(parent string) []string {
	runs, _ := filepath.Glob(filepath.Join(parent, "r*"))
	return runs
}

var sandboxInitCmd = func(args []string) error {
	return fmt.Errorf("ghafk __sandbox runs only inside a Linux sandbox")
}

func sandboxFirstPath() []string {
	var dirs []string
	execs, _ := developerDirs()
	for _, dev := range execs {
		if info, err := os.Stat(filepath.Join(dev, "usr", "bin")); err == nil && info.IsDir() {
			bin := filepath.Join(dev, "usr", "bin")
			dirs = append(dirs, bin)
		}
	}
	return append(dirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
}
