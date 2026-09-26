//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func launchdPaths() (plist, logFile string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		filepath.Join(home, "Library", "Logs", "ghafk.log"), nil
}

func launchdPrint() (string, error) {
	return run(".", "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
}

func start() error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	cfg, err := loadMachineSettings()
	if err != nil {
		return err
	}
	plist, logFile, err := launchdPaths()
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(plist), filepath.Dir(logFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := waitForIdleTick(); err != nil {
		return err
	}
	if err := os.WriteFile(plist, []byte(plistFor(exe, os.Getenv("PATH"), logFile, cfg.interval)), 0o644); err != nil {
		return err
	}
	_, _ = run(".", "launchctl", launchctlBootout(os.Getuid())...)
	if _, err := run(".", "launchctl", launchctlBootstrap(os.Getuid(), plist)...); err != nil {
		return err
	}
	fmt.Printf("ghafk: timer enabled, every %d minutes\n", cfg.interval)
	return nil
}

func stop() error {
	if err := waitForIdleTick(); err != nil {
		return err
	}
	if _, err := run(".", "launchctl", launchctlBootout(os.Getuid())...); err != nil {
		return err
	}
	fmt.Println("ghafk: timer disabled")
	return nil
}

func waitForIdleTick() error {
	announced := false
	for {
		out, err := launchdPrint()
		if err != nil || !launchdRunning(out) {
			return nil
		}
		if !announced {
			fmt.Println("ghafk: waiting for the running tick to finish")
			announced = true
		}
		time.Sleep(5 * time.Second)
	}
}

func schedulerStatus() {
	out, err := launchdPrint()
	if err != nil {
		fmt.Println("timer not loaded; run ghafk start")
	} else {
		fmt.Println(launchdSummary(out))
	}
	fmt.Println()
	_, logFile, err := launchdPaths()
	if err != nil {
		return
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	fmt.Println(strings.Join(lines, "\n"))
}
