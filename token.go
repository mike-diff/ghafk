package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const tokenWarnDays = 14

func ownerAccount(dir string) (string, time.Time, error) {
	out, err := ghOwner(dir, "api", "--include", "user", "--jq", ".login")
	if err != nil {
		return "", time.Time{}, err
	}
	login, exp := parseOwner(out)
	return login, exp, nil
}

func parseOwner(out string) (string, time.Time) {
	var exp time.Time
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "github-authentication-token-expiration") {
			continue
		}
		value = strings.TrimSpace(value)
		for _, layout := range []string{"2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 MST"} {
			if t, err := time.Parse(layout, value); err == nil {
				exp = t.UTC()
				break
			}
		}
	}
	return strings.TrimSpace(lines[len(lines)-1]), exp
}

func daysLeft(exp, now time.Time) int {
	return int(math.Floor(exp.Sub(now).Hours() / 24))
}

func tokenWarning(login string, exp, now time.Time) string {
	switch {
	case exp.IsZero():
		return ""
	case !exp.After(now):
		return fmt.Sprintf("warning: the GitHub token of %s expired on %s. Replace it; see docs/separate-user.md", login, exp.Format("2006-01-02"))
	case daysLeft(exp, now) <= tokenWarnDays:
		return fmt.Sprintf("warning: the GitHub token of %s expires in %d days, on %s. Replace it; see docs/separate-user.md", login, daysLeft(exp, now), exp.Format("2006-01-02"))
	}
	return ""
}

func tokenStatus(exp, now time.Time) string {
	if exp.IsZero() {
		return "github token: no expiry date"
	}
	if !exp.After(now) {
		return fmt.Sprintf("github token: expired %s", exp.Format("2006-01-02"))
	}
	return fmt.Sprintf("github token: expires %s (in %d days)", exp.Format("2006-01-02"), daysLeft(exp, now))
}
