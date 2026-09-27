//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func systemctl(args ...string) (string, error) {
	useUserRuntimeDir()
	return run(".", "systemctl", append([]string{"--user"}, args...)...)
}

func useUserRuntimeDir() {
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return
	}
	dir := fmt.Sprintf("/run/user/%d", os.Getuid())
	if _, err := os.Stat(dir); err == nil {
		os.Setenv("XDG_RUNTIME_DIR", dir)
	}
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
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(config, "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ghafk.service"), []byte(serviceFor(exe, os.Getenv("PATH"))), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ghafk.timer"), []byte(timerFor(cfg.interval)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "ghafk.timer"}, {"restart", "ghafk.timer"}} {
		if _, err := systemctl(args...); err != nil {
			return err
		}
	}
	fmt.Printf("ghafk: timer enabled, every %d minutes\n", cfg.interval)
	return nil
}

func stop() error {
	if _, err := systemctl("disable", "--now", "ghafk.timer"); err != nil {
		return err
	}
	fmt.Println("ghafk: timer disabled")
	return nil
}

func schedulerStatus() {
	for _, args := range [][]string{
		{"list-timers", "ghafk.timer", "--no-pager"},
		{"is-active", "ghafk.service"},
	} {
		out, err := systemctl(args...)
		fmt.Println(orError(out, err))
		fmt.Println()
	}
	out, err := run(".", "journalctl", "--user", "-u", "ghafk.service", "-n", "20", "--no-pager")
	fmt.Println(orError(out, err))
}
