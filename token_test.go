package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseOwnerReadsLoginAndExpiry(t *testing.T) {
	for _, header := range []string{
		"GitHub-Authentication-Token-Expiration: 2026-10-27 08:00:00 UTC",
		"github-authentication-token-expiration: 2026-10-27 01:00:00 -0700",
	} {
		login, exp := parseOwner("HTTP/2.0 200 OK\n" + header + "\nX-Other: 1\n\nsomeone")
		if login != "someone" {
			t.Fatalf("login = %q", login)
		}
		if want := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC); !exp.Equal(want) {
			t.Fatalf("%s: expiry = %v, want %v", header, exp, want)
		}
	}
}

func TestParseOwnerWithoutExpiry(t *testing.T) {
	login, exp := parseOwner("HTTP/2.0 200 OK\nX-Other: 1\n\nsomeone")
	if login != "someone" || !exp.IsZero() {
		t.Fatalf("login=%q expiry=%v, want no expiry", login, exp)
	}
}

func TestTokenWarningOnlyInTheLastTwoWeeks(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		exp  time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(15 * 24 * time.Hour), ""},
		{now.Add(14 * 24 * time.Hour), "expires in 14 days"},
		{now.Add(-time.Hour), "expired"},
	}
	for _, c := range cases {
		got := tokenWarning("someone", c.exp, now)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Fatalf("expiry %v: warning %q, want %q", c.exp, got, c.want)
		}
	}
}

func TestTokenStatusLine(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if got := tokenStatus(time.Time{}, now); got != "github token: no expiry date" {
		t.Fatalf("no expiry: %q", got)
	}
	if got := tokenStatus(now.Add(30*24*time.Hour), now); got != "github token: expires 2026-10-31 (in 30 days)" {
		t.Fatalf("with expiry: %q", got)
	}
}
