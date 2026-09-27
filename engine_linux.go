//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
)

const rootGroup = "root"

func unitPath(suffix string) string {
	return "/etc/systemd/system/" + engineUnitName + suffix
}

func installedEngine() (engineLayout, bool) {
	_, err := os.Stat(unitPath(".service"))
	return linuxEngine, err == nil
}

func inGroupDB(u *user.User, group string) bool {
	g, err := user.LookupGroup(group)
	if err != nil {
		return false
	}
	ids, err := u.GroupIds()
	return err == nil && slices.Contains(ids, g.Gid)
}

func engineSetup() error {
	l := linuxEngine
	if os.Geteuid() == 0 || isEngineAccount(l) {
		return fmt.Errorf("run `ghafk engine setup` from your own account; it asks sudo for each step")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("ghafk engine needs systemd")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	ghPath, err := systemTool("gh")
	if err != nil {
		return err
	}
	if _, err := systemTool("git"); err != nil {
		return err
	}
	name, email, err := personGitIdentity()
	if err != nil {
		return err
	}
	me, err := user.Current()
	if err != nil {
		return err
	}
	if out, _ := run(".", "systemctl", "--user", "is-enabled", "ghafk.timer"); out == "enabled" {
		return fmt.Errorf("your own ghafk timer is on; run `ghafk stop` first so that two engines do not work the same issues")
	}
	cfg, err := loadMachineSettings()
	if err != nil {
		return err
	}
	existing, lookupErr := user.Lookup(l.user)
	if lookupErr == nil && existing.HomeDir != l.home {
		return fmt.Errorf("an account named %s exists with home %s; ghafk engine setup manages only an account with home %s. Remove that account's timer and the account first, or keep using it by hand", l.user, existing.HomeDir, l.home)
	}

	fmt.Println("ghafk: sudo asks for your password once; each step then runs as its own sudo command.")
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")

	if lookupErr != nil {
		if _, err := runPrivileged("", "useradd", "--system", "--create-home", "--home-dir", l.home, "--shell", "/bin/bash", "--user-group", l.user); err != nil {
			return err
		}
		fmt.Printf("ghafk: created the %s account with home %s\n", l.user, l.home)
	}
	relogin := false
	if !inGroupDB(me, l.user) {
		if _, err := runPrivileged("", "usermod", "-aG", l.user, me.Username); err != nil {
			return err
		}
		relogin = true
	}
	if _, err := runPrivileged("", "install", "-d", "-o", l.user, "-g", l.user, "-m", "0750", l.config()); err != nil {
		return err
	}
	if self != l.bin {
		if _, err := runPrivileged("", "install", "-o", "root", "-g", rootGroup, "-m", "0755", self, l.bin); err != nil {
			return err
		}
	}
	if err := installAs(l, renderEngineGitconfig(name, email, ghPath), 0o644, l.user, filepath.Join(l.home, ".gitconfig")); err != nil {
		return err
	}
	if err := syncConfig(l, me.HomeDir); err != nil {
		return err
	}
	if !hasToken(l) {
		token, err := askToken(l)
		if err != nil {
			return err
		}
		if err := storeToken(l, token); err != nil {
			return err
		}
	}
	if err := installAs(l, engineServiceUnit(l), 0o644, "root", unitPath(".service")); err != nil {
		return err
	}
	if err := installAs(l, engineTimerUnit(cfg.interval), 0o644, "root", unitPath(".timer")); err != nil {
		return err
	}
	for _, args := range [][]string{{"systemctl", "daemon-reload"}, {"systemctl", "enable", "--now", engineUnitName + ".timer"}} {
		if _, err := runPrivileged("", args...); err != nil {
			return err
		}
	}
	list, err := runPrivileged("", sandboxArgs(l, l.bin, "harness", "list")...)
	if err != nil {
		fmt.Printf("ghafk: warning: could not list harnesses inside the engine sandbox: %v\n", err)
	}
	reportHarnesses(l, harnessesOnPath(list), cfg)
	fmt.Printf("ghafk: the engine runs as %s every %d minutes. `ghafk status` shows it.\n", l.user, cfg.interval)
	if relogin {
		fmt.Printf("ghafk: log out and in again so that your new membership in the %s group lets `ghafk status` read the engine's files.\n", l.user)
	}
	return nil
}

func reportHarnesses(l engineLayout, found []string, cfg settings) {
	if len(found) == 0 {
		fmt.Printf("ghafk: warning: the engine finds no harness. Install one as the engine account (`sudo -iu %s`, then the harness's own install and login), close that shell, and run `ghafk engine setup` again.\n", l.user)
		return
	}
	fmt.Printf("ghafk: harnesses on the engine's PATH: %s\n", strings.Join(found, ", "))
}

func engineStart() error {
	if _, ok := installedEngine(); !ok {
		return fmt.Errorf("no engine is set up; run `ghafk engine setup`")
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	_, err := runPrivileged("", "systemctl", "enable", "--now", engineUnitName+".timer")
	return err
}

func engineStop() error {
	if _, ok := installedEngine(); !ok {
		return fmt.Errorf("no engine is set up")
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	_, err := runPrivileged("", "systemctl", "disable", "--now", engineUnitName+".timer")
	if err == nil {
		fmt.Println("ghafk: the engine timer is off; a running tick finishes first")
	}
	return err
}

func engineRemove(purge bool) error {
	l := linuxEngine
	me, err := user.Current()
	if err != nil {
		return err
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	runPrivileged("", "systemctl", "disable", "--now", engineUnitName+".timer")
	runPrivileged("", "systemctl", "stop", engineUnitName+".service")
	if _, err := runPrivileged("", "rm", "-f", unitPath(".service"), unitPath(".timer"), l.bin); err != nil {
		return err
	}
	if _, err := runPrivileged("", "systemctl", "daemon-reload"); err != nil {
		return err
	}
	fmt.Println("ghafk: removed the engine service and binary")
	if !purge {
		fmt.Printf("ghafk: kept the %s account and %s; `ghafk engine remove --purge` deletes them\n", l.user, l.home)
		return nil
	}
	runPrivileged("", "gpasswd", "-d", me.Username, l.user)
	if _, err := runPrivileged("", "userdel", "--remove", l.user); err != nil {
		return err
	}
	fmt.Printf("ghafk: deleted the %s account and %s\n", l.user, l.home)
	return nil
}

func engineServiceState(l engineLayout) string {
	out, err := run(".", "systemctl", "list-timers", engineUnitName+".timer", "--no-pager")
	state, _ := run(".", "systemctl", "is-active", engineUnitName+".service")
	return orError(out, err) + "\nengine tick: " + state
}

func reexecWithGroup(l engineLayout) bool {
	me, err := user.Current()
	if err != nil || os.Getenv("GHAFK_SG") != "" || !inGroupDB(me, l.user) {
		return false
	}
	self, err := selfPath()
	if err != nil {
		return false
	}
	cmd := exec.Command("sg", l.user, "-c", "'"+strings.ReplaceAll(self, "'", `'\''`)+"' status")
	cmd.Env = append(os.Environ(), "GHAFK_SG=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run() == nil
}
