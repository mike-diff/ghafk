package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

const launchdLabel = "ghafk.tick"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>tick</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func plistFor(exe, path, logFile string, minutes int) string {
	return fmt.Sprintf(plistTemplate, launchdLabel, xmlText(exe), xmlText(path), minutes*60, xmlText(logFile), xmlText(logFile))
}

func launchctlBootstrap(uid int, plist string) []string {
	return []string{"bootstrap", fmt.Sprintf("gui/%d", uid), plist}
}

func launchctlBootout(uid int) []string {
	return []string{"bootout", fmt.Sprintf("gui/%d/%s", uid, launchdLabel)}
}

func launchdRunning(print string) bool {
	for _, line := range strings.Split(print, "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true
		}
	}
	return false
}

func launchdSummary(print string) string {
	var keep []string
	for _, line := range strings.Split(print, "\n") {
		line = strings.TrimSpace(line)
		for _, key := range []string{"state = ", "runs = ", "last exit code = ", "run interval = "} {
			if strings.HasPrefix(line, key) {
				keep = append(keep, line)
			}
		}
	}
	return strings.Join(keep, "\n")
}
