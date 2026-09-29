package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if userHoldsLogin(id.Login) {
		fmt.Fprintf(os.Stderr, "ghafk: a user account is named %s like the app, so its comments could pass as ghafk's; acting as %s\n", id.Login, owner)
		return "", owner
	}
	return id.Token, id.Login
}

var loginTypes = map[string]string{}

func userHoldsLogin(login string) bool {
	typ, seen := loginTypes[login]
	if !seen {
		out, err := ghOwner(".", "api", "users/"+login, "--jq", ".type")
		typ = strings.TrimSpace(out)
		if err != nil && !strings.Contains(err.Error(), "404") {
			typ = "unknown"
		}
		loginTypes[login] = typ
	}
	return typ == "User" || typ == "Organization" || typ == "unknown"
}

func appMinter() func(repo string) (ghapp.Identity, error) {
	dir, err := configDir()
	if err != nil {
		return func(string) (ghapp.Identity, error) { return ghapp.Identity{}, err }
	}
	registerSecretFile(filepath.Join(dir, "app.pem"))
	return ghapp.Minter(dir)
}
