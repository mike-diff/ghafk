package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

func TestEngineIdentityFallsBackToTheOwner(t *testing.T) {
	token, login := engineIdentity("owner", func() (ghapp.Identity, error) { return ghapp.Identity{}, errors.New("network down") })
	if token != "" || login != "owner" {
		t.Fatalf("a failed app token must leave the owner login, got token %q login %q", token, login)
	}
	loginTypes = map[string]string{}
	fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "api users/ghafk") {
			return "", errors.New("gh: Not Found (HTTP 404)")
		}
		return "", nil
	})
	token, login = engineIdentity("owner", func() (ghapp.Identity, error) { return ghapp.Identity{Token: "t", Login: "ghafk"}, nil })
	if token != "t" || login != "ghafk" {
		t.Fatalf("a minted app token must be used, got token %q login %q", token, login)
	}
}

func TestAUserNamedLikeTheAppStopsTheAppIdentity(t *testing.T) {
	loginTypes = map[string]string{}
	fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "api users/ghafk") {
			return "User", nil
		}
		return "", nil
	})
	token, login := engineIdentity("owner", func() (ghapp.Identity, error) { return ghapp.Identity{Token: "t", Login: "ghafk"}, nil })
	if token != "" || login != "owner" {
		t.Fatalf("token %q login %q; a user named like the app could post comments ghafk trusts as its own", token, login)
	}
}

func TestConsumedCountsTheBotsReaction(t *testing.T) {
	fakeGH(t, func(cmd string) (string, error) {
		firstPage := strings.Repeat("outsider\n", 30)
		if !strings.Contains(cmd, "--paginate") || !strings.Contains(cmd, "content=%2B1") {
			return firstPage, nil
		}
		return firstPage + "ghafk[bot]\n", nil
	})
	c := &prComment{URL: "https://github.com/o/r/issues/1#issuecomment-123"}
	if !consumed("repo", c, "ghafk") {
		t.Fatal("the bot's 👍 behind 30 other reactions was not seen, so the command runs again every tick")
	}
}
