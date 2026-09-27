package main

import (
	"fmt"
	"strconv"
	"strings"
)

const engineLabel = "ghafk.engine"

var darwinEngine = engineLayout{user: "_ghafk", home: "/usr/local/var/ghafk", bin: "/usr/local/bin/ghafk", etc: "/usr/local/etc/ghafk"}

func enginePathDarwin(home string) string {
	return strings.Join([]string{home + "/.local/bin", home + "/.local/share/pnpm", home + "/.npm-global/bin", home + "/go/bin",
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
}

func enginePlist(l engineLayout, minutes int) string {
	log := xmlText(l.home + "/.ghafk/launchd.log")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + engineLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(l.bin) + `</string>
		<string>tick</string>
	</array>
	<key>UserName</key>
	<string>` + xmlText(l.user) + `</string>
	<key>GroupName</key>
	<string>` + xmlText(l.user) + `</string>
	<key>WorkingDirectory</key>
	<string>` + xmlText(l.home) + `</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlText(enginePathDarwin(l.home)) + `</string>
		<key>` + configDirEnv + `</key>
		<string>` + xmlText(l.etc) + `</string>
	</dict>
	<key>Umask</key>
	<integer>23</integer>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(minutes*60) + `</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + log + `</string>
	<key>StandardErrorPath</key>
	<string>` + log + `</string>
</dict>
</plist>
`
}

func freeServiceID(users, groups string) (int, error) {
	used := map[int]bool{}
	for _, list := range []string{users, groups} {
		for _, line := range strings.Split(list, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			if id, err := strconv.Atoi(f[len(f)-1]); err == nil {
				used[id] = true
			}
		}
	}
	for id := 499; id >= 300; id-- {
		if !used[id] {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no free user and group id between 300 and 499")
}
