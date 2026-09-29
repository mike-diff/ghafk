//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const sandboxTool = "bwrap"

const usernsRestrictionFile = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

const sandboxUsernsFix = `Ubuntu 24.04 restricts unprivileged user namespaces, so bubblewrap cannot start. Give bubblewrap an unconfined AppArmor profile once, which lets it create user namespaces:

  sudo tee /etc/apparmor.d/bubblewrap >/dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>

profile bubblewrap /usr/bin/bwrap flags=(unconfined) {
userns,

include if exists <local/bubblewrap>
}
PROFILE
  sudo apparmor_parser -r /etc/apparmor.d/bubblewrap`

func runSuffix() string { return "" }

func runParent(work string) string { return work }

func sharedSandboxCache() bool { return true }

func staleRuns(string) []string { return nil }

func usernsRestricted() bool {
	data, err := os.ReadFile(usernsRestrictionFile)
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

func probeSandbox() error {
	if _, err := exec.LookPath(sandboxTool); err != nil {
		return fmt.Errorf("the sandbox needs bubblewrap: %v. Install the bubblewrap package", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandboxTool,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-user", "--disable-userns", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--unshare-net",
		"--die-with-parent", "--new-session",
		"--", "/bin/true")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if usernsRestricted() {
			return fmt.Errorf("the sandbox does not start (%.200s).\n%s", strings.TrimSpace(out.String()), sandboxUsernsFix)
		}
		return fmt.Errorf("the sandbox does not start: %.200s", strings.TrimSpace(out.String()))
	}
	return nil
}

func proxyStart(proxy *egressProxy, spec sandboxSpec) error {
	return proxy.listen("unix", proxySocketPath(spec.sockDir))
}

func relayPort() int {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return 20000 + int(binary.BigEndian.Uint16(b[:]))%40000
}

func sandboxLaunch(o sandboxOpts, spec sandboxSpec, proxy *egressProxy, command string, stdout, stderr io.Writer) error {
	port := relayPort()
	args := bwrapArgs(spec, port, o.dir, command)
	env := sandboxEnv(spec, spec.personHome, "http://127.0.0.1:"+strconv.Itoa(port))
	stopLocal, err := startLocalRelays(spec)
	if err != nil {
		return fmt.Errorf("%s: %w", o.name, err)
	}
	defer stopLocal()
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandboxTool, args...)
	cmd.Dir = "/"
	cmd.Stdin = strings.NewReader(o.stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = pipeGrace
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return nil
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", o.name, err)
	}
	return nil
}

func bwrapArgs(spec sandboxSpec, port int, dir, command string) []string {
	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/run",
		"--tmpfs", "/tmp",
		"--tmpfs", spec.homeHide,
	}
	if spec.homeReal != "" && !underTree(spec.homeReal, spec.homeHide) {
		args = append(args, "--tmpfs", spec.homeReal)
	}
	args = append(args, "--bind", spec.runHome, spec.personHome)
	for _, dir := range spec.programsUnderHome() {
		args = append(args, "--ro-bind", dir, dir)
	}
	for _, dir := range spec.extraBinds {
		args = append(args, "--ro-bind", dir, dir)
	}
	if spec.repoGit != "" {
		args = append(args, "--ro-bind", spec.repoGit, spec.repoGit)
	}
	args = append(args,
		"--bind", spec.runDir, spec.runDir,
		"--bind", spec.cacheDir, spec.cacheDir,
		"--bind", spec.sockDir, spec.sockDir)
	if spec.workDir != "" && spec.workDir != spec.runHome && spec.workDir != spec.personHome && spec.workDir != spec.homeHide {
		args = append(args, "--bind", spec.workDir, spec.workDir)
	}
	if spec.ghafkBin != "" && underHiddenTree(spec, spec.ghafkBin) {
		args = append(args, "--ro-bind", spec.ghafkBin, spec.ghafkBin)
	}
	args = append(args,
		"--unshare-user", "--disable-userns", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--unshare-net",
		"--die-with-parent", "--new-session",
		"--chdir", dir, "--",
		spec.ghafkBin, "__sandbox", proxySocketPath(spec.sockDir), strconv.Itoa(port))
	for _, local := range spec.localPorts {
		args = append(args, localSocketPath(spec.sockDir, local), strconv.Itoa(local))
	}
	return append(args, "--", "/bin/sh", "-c", command)
}

func startLocalRelays(spec sandboxSpec) (func(), error) {
	var listeners []net.Listener
	stop := func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}
	for _, port := range spec.localPorts {
		ln, err := net.Listen("unix", localSocketPath(spec.sockDir, port))
		if err != nil {
			stop()
			return nil, fmt.Errorf("the relay to local port %d cannot listen: %v", port, err)
		}
		listeners = append(listeners, ln)
		go relayAccept(ln, "tcp", "127.0.0.1:"+strconv.Itoa(port))
	}
	return stop, nil
}

func relayAccept(ln net.Listener, network, target string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			out, err := net.Dial(network, target)
			if err != nil {
				return
			}
			defer out.Close()
			copyBoth(conn, out)
		}(conn)
	}
}

func underHiddenTree(spec sandboxSpec, path string) bool {
	for _, tree := range spec.hiddenTrees() {
		if underTree(path, tree) {
			return true
		}
	}
	return false
}

func sandboxInitCmd(args []string) error {
	sep := slices.Index(args, "--")
	if sep < 2 || sep%2 != 0 || sep == len(args)-1 {
		return fmt.Errorf("usage: ghafk __sandbox SOCKET PORT [SOCKET PORT...] -- COMMAND [ARGS...]")
	}
	for i := 0; i < sep; i += 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:"+args[i+1])
		if err != nil {
			return fmt.Errorf("the sandbox relay cannot listen: %v", err)
		}
		go relayAccept(ln, "unix", args[i])
	}
	rest := args[sep+1:]
	child := exec.Command(rest[0], rest[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := child.Run()
	if runErr == nil {
		return nil
	}
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		return runErr
	}
	code := exitErr.ExitCode()
	if code < 0 {
		code = 1
	}
	os.Exit(code)
	return nil
}

func sandboxFirstPath() []string { return nil }

func copyBoth(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
}

func (s *sandboxSpec) programsUnderHome() []string {
	var dirs []string
	for _, dir := range s.programDirs {
		if underHiddenTree(*s, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (s *sandboxSpec) hiddenTrees() []string {
	trees := []string{s.homeHide}
	if s.homeReal != "" {
		trees = append(trees, s.homeReal)
	}
	return append(trees, "/tmp", "/run")
}

func localSocketPath(runDir string, port int) string {
	return filepath.Join(runDir, "l"+strconv.Itoa(port)+".sock")
}
