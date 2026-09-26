package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

func engineIdentity(owner string, mint func() (ghapp.Identity, error)) (string, string) {
	id, err := mint()
	if errors.Is(err, ghapp.ErrNoApp) {
		return "", owner
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: app token unavailable, acting as %s: %v\n", owner, err)
		return "", owner
	}
	return id.Token, id.Login
}

func appMinter() func(repo string) (ghapp.Identity, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return func(string) (ghapp.Identity, error) { return ghapp.Identity{}, err }
	}
	return ghapp.Minter(filepath.Join(home, ".ghafk"))
}
