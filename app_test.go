package main

import (
	"errors"
	"testing"

	"github.com/mike-diff/ghafk/internal/ghapp"
)

func TestEngineIdentityFallsBackToTheOwner(t *testing.T) {
	token, login := engineIdentity("owner", func() (ghapp.Identity, error) { return ghapp.Identity{}, errors.New("network down") })
	if token != "" || login != "owner" {
		t.Fatalf("a failed app token must leave the owner login, got token %q login %q", token, login)
	}
	token, login = engineIdentity("owner", func() (ghapp.Identity, error) { return ghapp.Identity{Token: "t", Login: "ghafk"}, nil })
	if token != "t" || login != "ghafk" {
		t.Fatalf("a minted app token must be used, got token %q login %q", token, login)
	}
}

func TestConsumedCountsTheBotsReaction(t *testing.T) {
	fakeGH(t, func(cmd string) (string, error) {
		return `[{"content":"+1","user":{"login":"ghafk[bot]"}}]`, nil
	})
	c := &prComment{URL: "https://github.com/o/r/issues/1#issuecomment-123"}
	if !consumed("repo", c, "ghafk") {
		t.Fatal("the bot's own 👍 was not recognized, so every command would run again each tick")
	}
}
