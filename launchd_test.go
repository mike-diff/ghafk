package main

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
)

func TestPlistRunsThisBinaryEveryTwoMinutesWithTheCallersPath(t *testing.T) {
	plist := plistFor("/Users/me/go/bin/ghafk", "/opt/homebrew/bin:/usr/bin", "/Users/me/Library/Logs/ghafk.log", 2)
	for _, want := range []string{
		"<key>Label</key>\n\t<string>" + launchdLabel + "</string>",
		"<string>/Users/me/go/bin/ghafk</string>\n\t\t<string>tick</string>",
		"<key>PATH</key>\n\t\t<string>/opt/homebrew/bin:/usr/bin</string>",
		"<key>StartInterval</key>\n\t<integer>120</integer>",
		"<key>StandardOutPath</key>\n\t<string>/Users/me/Library/Logs/ghafk.log</string>",
		"<key>StandardErrorPath</key>\n\t<string>/Users/me/Library/Logs/ghafk.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist is missing %q:\n%s", want, plist)
		}
	}
}

func TestPlistIsWellFormedXMLWhenPathsHoldMarkup(t *testing.T) {
	plist := plistFor("/Users/a&b/ghafk", "/x<y>/bin", "/Users/a&b/log", 2)
	dec := xml.NewDecoder(strings.NewReader(plist))
	dec.Strict = true
	for {
		if _, err := dec.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("plist is not well-formed XML: %v\n%s", err, plist)
		}
	}
	if !strings.Contains(plist, "/Users/a&amp;b/ghafk") {
		t.Fatalf("ampersand is not escaped:\n%s", plist)
	}
}

func TestLaunchctlLoadsAndUnloadsTheAgentInTheUserSession(t *testing.T) {
	if got, want := launchctlBootstrap(501, "/Users/me/Library/LaunchAgents/x.plist"), []string{"bootstrap", "gui/501", "/Users/me/Library/LaunchAgents/x.plist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap args %v, want %v", got, want)
	}
	if got, want := launchctlBootout(501), []string{"bootout", "gui/501/" + launchdLabel}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bootout args %v, want %v", got, want)
	}
}

func TestLaunchdTickIsRunningOnlyWhenTheJobStateIsRunning(t *testing.T) {
	running := "gui/501/ghafk.tick = {\n\tactive count = 1\n\tstate = running\n\tpid = 4242\n}"
	waiting := "gui/501/ghafk.tick = {\n\tactive count = 0\n\tstate = not running\n\truns = 7\n}"
	if !launchdRunning(running) {
		t.Fatal("a running tick was reported idle, so stop would kill it mid-run")
	}
	if launchdRunning(waiting) {
		t.Fatal("an idle agent was reported running, so stop would wait forever")
	}
}

func TestLaunchdSummaryKeepsOnlyTheStateLines(t *testing.T) {
	print := "gui/501/ghafk.tick = {\n\tactive count = 0\n\tpath = /Users/me/Library/LaunchAgents/ghafk.tick.plist\n\tstate = not running\n\truns = 7\n\tlast exit code = 0\n\trun interval = 120 seconds\n\tenvironment = {\n\t\tPATH => /usr/bin\n\t}\n}"
	got := launchdSummary(print)
	want := "state = not running\nruns = 7\nlast exit code = 0\nrun interval = 120 seconds"
	if got != want {
		t.Fatalf("summary:\n%s\nwant:\n%s", got, want)
	}
}
