package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

func status() error {
	if l, ok := installedEngine(); ok && !isEngineAccount(l) {
		return engineStatusView(l)
	}
	schedulerStatus()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := loadEngineEnv(); err != nil {
		return err
	}
	cfg, err := loadMachineSettings()
	if err != nil {
		return err
	}
	targets, err := engineRepos(home, cfg.skip, cfg.repos, false)
	if err != nil {
		return err
	}
	account, err := engineAccount(targets)
	if err != nil {
		account = "engine account: unknown: " + err.Error()
	}
	fmt.Println(account)
	repoStates(targets)
	return nil
}

func engineAccount(targets []target) (string, error) {
	owner, expires, err := ownerAccount(".")
	if err != nil {
		return "", err
	}
	fmt.Println(tokenStatus(expires, time.Now()))
	if len(targets) == 0 {
		return fmt.Sprintf("engine account: %s (owner gh login; no repository to check an app on)", owner), nil
	}
	name := targets[0].name
	mint := appMinter()
	if token, login := engineIdentity(owner, func() (ghapp.Identity, error) { return mint(name) }); token != "" {
		return fmt.Sprintf("engine account: %s[bot] (GitHub App, checked on %s)", login, name), nil
	}
	return fmt.Sprintf("engine account: %s (owner gh login)", owner), nil
}

func repoStates(targets []target) {
	for _, t := range targets {
		fmt.Printf("%s: %s\n", filepath.Base(t.path), repoState(t.path))
	}
}

func repoState(repo string) string {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		return "found on GitHub; the next tick clones it"
	}
	henv, err := loadHarnessEnv()
	if err != nil {
		return "not ready: " + err.Error()
	}
	if _, err := loadWorkflow(repo, henv); err != nil {
		return "not ready: " + err.Error()
	}
	return "ready"
}
