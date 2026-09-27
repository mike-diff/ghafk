package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeSettings(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSettingsDefaultToEveryColumnAndTwoMinutes(t *testing.T) {
	s, err := loadSettings(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.progress, defaultColumns) || s.interval != 2 {
		t.Fatalf("defaults = %v every %d minutes, want the default columns every 2 minutes", s.progress, s.interval)
	}
}

func TestSettingsReadProgressColumnsAndInterval(t *testing.T) {
	s, err := loadSettings(writeSettings(t, "default: pi m\nprogress: step status tokens\ninterval: 15\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.progress, []string{"step", "status", "tokens"}) || s.interval != 15 {
		t.Fatalf("got %v every %d minutes", s.progress, s.interval)
	}
}

func TestSettingsCollectEverySkipLine(t *testing.T) {
	s, err := loadSettings(writeSettings(t, "skip: me/old\nskip: me/private\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.skip, []string{"me/old", "me/private"}) {
		t.Fatalf("skip = %v, want both repositories", s.skip)
	}
}

func TestSettingsRejectMistakes(t *testing.T) {
	for _, text := range []string{
		"progress: step status harnes\n",
		"progress: status tokens\n",
		"interval: 7\n",
		"interval: 0\n",
		"interval: two\n",
		"skip: private\n",
	} {
		if _, err := loadSettings(writeSettings(t, text)); err == nil {
			t.Fatalf("%q was accepted; a typo would silently change the card or the timer", strings.TrimSpace(text))
		}
	}
}

func TestProgressTableShowsOnlyTheConfiguredColumns(t *testing.T) {
	useColumns(t, "step", "status", "tokens")
	st := cardState{Number: 9, Title: "t", Phase: "working", Steps: map[string]cardStep{
		"Groom": {Status: "done", Started: "2026-09-26T16:38:00Z", Tokens: 115000, Harness: "secret-harness secret-model"},
	}}
	body := renderCard(st, time.UTC)
	if !strings.Contains(body, "| Step | Status | Tokens |\n|---|---|---|\n") {
		t.Fatalf("header does not match the configured columns:\n%s", body)
	}
	if !strings.Contains(body, "| Groom | ✅ | 115k |") {
		t.Fatalf("row does not match the configured columns:\n%s", body)
	}
	if strings.Contains(body, "secret-harness") || strings.Contains(body, "Harness") {
		t.Fatalf("a hidden column still shows:\n%s", body)
	}
}

func TestTimerAndAgentUseTheConfiguredInterval(t *testing.T) {
	if unit := timerFor(15); !strings.Contains(unit, "OnCalendar=*:0/15\n") {
		t.Fatalf("timer does not tick every 15 minutes:\n%s", unit)
	}
	if plist := plistFor("/g", "/usr/bin", "/log", 15); !strings.Contains(plist, "<key>StartInterval</key>\n\t<integer>900</integer>") {
		t.Fatalf("agent does not tick every 900 seconds:\n%s", plist)
	}
}

func TestFinishingAStepRecordsItsEndTime(t *testing.T) {
	c := &card{st: cardState{Step: "Groom", Steps: map[string]cardStep{"Groom": {Status: "running", Started: "2026-09-26T16:38:00Z"}}}}
	c.mark("done", 0)
	if c.st.Steps["Groom"].Ended == "" {
		t.Fatal("a finished step has no end time, so its duration cannot show")
	}
}

func TestDurationColumnShowsHowLongEachStepTook(t *testing.T) {
	cols := []string{"step", "ended", "duration"}
	for _, tc := range []struct {
		started, ended, want string
	}{
		{"2026-09-26T16:38:00Z", "2026-09-26T16:38:45Z", "| Groom | Sep 26 16:38 UTC | 45s |"},
		{"2026-09-26T16:38:00Z", "2026-09-26T16:40:14Z", "| Groom | Sep 26 16:40 UTC | 2m 14s |"},
		{"2026-09-26T16:38:00Z", "2026-09-26T17:41:00Z", "| Groom | Sep 26 17:41 UTC | 1h 03m |"},
		{"2026-09-26T16:38:00Z", "", "| Groom |  |  |"},
	} {
		s := cardStep{Status: "done", Started: tc.started, Ended: tc.ended}
		if got := renderStep("Groom", 0, s, time.UTC, cols); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestProgressDefaultsToDurationInsteadOfStartTime(t *testing.T) {
	s, err := loadSettings(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"step", "status", "duration", "tokens", "harness"}; !reflect.DeepEqual(s.progress, want) {
		t.Fatalf("default columns %v, want %v", s.progress, want)
	}
	if _, err := loadSettings(writeSettings(t, "progress: step started ended duration\n")); err != nil {
		t.Fatalf("started, ended and duration are all valid columns: %v", err)
	}
}
