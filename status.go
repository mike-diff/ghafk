package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

func status() error {
	schedulerStatus()
	account, err := engineAccount()
	if err != nil {
		account = "engine account: unknown: " + err.Error()
	}
	fmt.Println(account)
	return repoStates()
}

func engineAccount() (string, error) {
	owner, expires, err := ownerAccount(".")
	if err != nil {
		return "", err
	}
	fmt.Println(tokenStatus(expires, time.Now()))
	path, err := reposFile()
	if err != nil {
		return "", err
	}
	repos, err := readRepos(path)
	if err != nil || len(repos) == 0 {
		return fmt.Sprintf("engine account: %s (owner gh login; no registered repository to check an app on)", owner), err
	}
	name, err := ghOwner(repos[0], "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return "", err
	}
	mint := appMinter()
	if token, login := engineIdentity(owner, func() (ghapp.Identity, error) { return mint(name) }); token != "" {
		return fmt.Sprintf("engine account: %s[bot] (GitHub App, checked on %s)", login, name), nil
	}
	return fmt.Sprintf("engine account: %s (owner gh login)", owner), nil
}

func repoStates() error {
	path, err := reposFile()
	if err != nil {
		return err
	}
	repos, err := readRepos(path)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		fmt.Printf("%s: %s\n", filepath.Base(repo), repoState(repo))
	}
	return nil
}

func repoState(repo string) string {
	henv, err := loadHarnessEnv()
	if err != nil {
		return "not ready: " + err.Error()
	}
	if _, err := loadWorkflow(repo, henv); err != nil {
		return "not ready: " + err.Error()
	}
	return "ready"
}
