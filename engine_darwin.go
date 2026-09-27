//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const rootGroup = "wheel"

const enginePlistPath = "/Library/LaunchDaemons/" + engineLabel + ".plist"

func installedEngine() (engineLayout, bool) {
	_, err := os.Stat(enginePlistPath)
	return darwinEngine, err == nil
}

func accountHome(name string) (string, bool) {
	out, err := run(".", "dscl", ".", "-read", "/Users/"+name, "NFSHomeDirectory")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(out, "NFSHomeDirectory:")), true
}

func inEngineGroup(person, group string) bool {
	_, err := run(".", "dseditgroup", "-o", "checkmember", "-m", person, group)
	return err == nil
}

func createDarwinAccount(l engineLayout) error {
	users, err := run(".", "dscl", ".", "-list", "/Users", "UniqueID")
	if err != nil {
		return err
	}
	groups, err := run(".", "dscl", ".", "-list", "/Groups", "PrimaryGroupID")
	if err != nil {
		return err
	}
	id, err := freeServiceID(users, groups)
	if err != nil {
		return err
	}
	ids := strconv.Itoa(id)
	for _, args := range [][]string{
		{"dscl", ".", "-create", "/Groups/" + l.user},
		{"dscl", ".", "-create", "/Groups/" + l.user, "PrimaryGroupID", ids},
		{"dscl", ".", "-create", "/Groups/" + l.user, "RealName", "ghafk engine"},
		{"dscl", ".", "-create", "/Groups/" + l.user, "Password", "*"},
		{"dscl", ".", "-create", "/Users/" + l.user},
		{"dscl", ".", "-create", "/Users/" + l.user, "UniqueID", ids},
		{"dscl", ".", "-create", "/Users/" + l.user, "PrimaryGroupID", ids},
		{"dscl", ".", "-create", "/Users/" + l.user, "UserShell", "/bin/zsh"},
		{"dscl", ".", "-create", "/Users/" + l.user, "NFSHomeDirectory", l.home},
		{"dscl", ".", "-create", "/Users/" + l.user, "RealName", "ghafk engine"},
		{"dscl", ".", "-create", "/Users/" + l.user, "IsHidden", "1"},
		{"dscl", ".", "-create", "/Users/" + l.user, "Password", "*"},
		{"mkdir", "-p", filepath.Dir(l.home)},
		{"install", "-d", "-o", l.user, "-g", l.user, "-m", "0750", l.home},
	} {
		if _, err := runPrivileged("", args...); err != nil {
			return err
		}
	}
	return nil
}

func engineLoaded() bool {
	_, err := run(".", "launchctl", "print", "system/"+engineLabel)
	return err == nil
}

func waitForIdleEngine() error {
	for i := 0; i < 240; i++ {
		out, err := run(".", "launchctl", "print", "system/"+engineLabel)
		if err != nil || !launchdRunning(out) {
			return nil
		}
		if i == 0 {
			fmt.Println("ghafk: waiting for the running engine tick to finish")
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("the engine tick is still running after 20 minutes; try again later")
}

func engineSetup() error {
	l := darwinEngine
	if os.Geteuid() == 0 || isEngineAccount(l) {
		return fmt.Errorf("run `ghafk engine setup` from your own account; it asks sudo for each step")
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
	if _, err := launchdPrint(); err == nil {
		return fmt.Errorf("your own ghafk timer is on; run `ghafk stop` first so that two engines do not work the same issues")
	}
	cfg, err := loadMachineSettings()
	if err != nil {
		return err
	}
	home, exists := accountHome(l.user)
	if exists && home != l.home {
		return fmt.Errorf("an account named %s exists with home %s; ghafk engine setup manages only an account with home %s", l.user, home, l.home)
	}

	fmt.Println("ghafk: sudo asks for your password once; each step then runs as its own sudo command.")
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")

	if !exists {
		if err := createDarwinAccount(l); err != nil {
			return err
		}
		fmt.Printf("ghafk: created the hidden %s account with home %s\n", l.user, l.home)
	}
	relogin := false
	if !inEngineGroup(me.Username, l.user) {
		if _, err := runPrivileged("", "dseditgroup", "-o", "edit", "-a", me.Username, "-t", "user", l.user); err != nil {
			return err
		}
		relogin = true
	}
	if _, err := runPrivileged("", "install", "-d", "-o", l.user, "-g", l.user, "-m", "0750", l.config()); err != nil {
		return err
	}
	if self != l.bin {
		if _, err := runPrivileged("", "mkdir", "-p", filepath.Dir(l.bin)); err != nil {
			return err
		}
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
	plist := enginePlist(l, cfg.interval)
	current, _ := os.ReadFile(enginePlistPath)
	if string(current) != plist || !engineLoaded() {
		if err := waitForIdleEngine(); err != nil {
			return err
		}
		runPrivileged("", "launchctl", "bootout", "system/"+engineLabel)
		if err := installAs(l, plist, 0o644, "root", enginePlistPath); err != nil {
			return err
		}
		if _, err := runPrivileged("", "launchctl", "bootstrap", "system", enginePlistPath); err != nil {
			return err
		}
	}
	list, err := runPrivileged("", "-u", l.user, "-H", "env", "-i", "HOME="+l.home, "USER="+l.user, "PATH="+enginePathDarwin(l.home), l.bin, "harness", "list")
	if err != nil {
		fmt.Printf("ghafk: warning: could not list the engine's harnesses: %v\n", err)
	}
	reportHarnesses(l, harnessesOnPath(list))
	fmt.Printf("ghafk: the engine runs as %s every %d minutes. `ghafk status` shows it.\n", l.user, cfg.interval)
	if relogin {
		fmt.Printf("ghafk: open a new terminal so that your new membership in the %s group lets `ghafk status` read the engine's files.\n", l.user)
	}
	return nil
}

func engineStart() error {
	if _, ok := installedEngine(); !ok {
		return fmt.Errorf("no engine is set up; run `ghafk engine setup`")
	}
	if engineLoaded() {
		return nil
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	_, err := runPrivileged("", "launchctl", "bootstrap", "system", enginePlistPath)
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
	if err := waitForIdleEngine(); err != nil {
		return err
	}
	if _, err := runPrivileged("", "launchctl", "bootout", "system/"+engineLabel); err != nil && engineLoaded() {
		return err
	}
	fmt.Println("ghafk: the engine timer is off")
	return nil
}

func engineRemove(purge bool) error {
	l := darwinEngine
	me, err := user.Current()
	if err != nil {
		return err
	}
	if err := sudoValidate(); err != nil {
		return err
	}
	defer runPrivileged("", "-k")
	if err := waitForIdleEngine(); err != nil {
		return err
	}
	runPrivileged("", "launchctl", "bootout", "system/"+engineLabel)
	if _, err := runPrivileged("", "rm", "-f", enginePlistPath, l.bin); err != nil {
		return err
	}
	fmt.Println("ghafk: removed the engine service and binary")
	if !purge {
		fmt.Printf("ghafk: kept the %s account and %s; `ghafk engine remove --purge` deletes them\n", l.user, l.home)
		return nil
	}
	runPrivileged("", "dseditgroup", "-o", "edit", "-d", me.Username, "-t", "user", l.user)
	for _, args := range [][]string{{"dscl", ".", "-delete", "/Users/" + l.user}, {"dscl", ".", "-delete", "/Groups/" + l.user}, {"rm", "-rf", l.home}} {
		if _, err := runPrivileged("", args...); err != nil {
			return err
		}
	}
	fmt.Printf("ghafk: deleted the %s account and %s\n", l.user, l.home)
	return nil
}

func engineServiceState(l engineLayout) string {
	out, err := run(".", "launchctl", "print", "system/"+engineLabel)
	if err != nil {
		return "engine timer: not loaded"
	}
	return launchdSummary(out)
}

func reexecWithGroup(engineLayout) bool { return false }
