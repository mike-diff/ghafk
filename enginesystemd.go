package main

import (
	"fmt"
	"strings"
)

const engineUnitName = "ghafk-engine"

var linuxEngine = engineLayout{user: "ghafk", home: "/var/lib/ghafk", bin: "/usr/local/bin/ghafk", etc: "/etc/ghafk"}

func engineHardening(home string) []string {
	return []string{
		"UMask=0027",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"ReadWritePaths=" + home,
		"ProtectHome=true",
		"PrivateTmp=yes",
		"PrivateDevices=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectKernelLogs=yes",
		"ProtectControlGroups=yes",
		"ProtectClock=yes",
		"ProtectHostname=yes",
		"ProtectProc=invisible",
		"RestrictSUIDSGID=yes",
		"RestrictRealtime=yes",
		"LockPersonality=yes",
		"SystemCallArchitectures=native",
		"CapabilityBoundingSet=",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
	}
}

func enginePath(home string) string {
	return strings.Join([]string{
		home + "/.local/bin",
		home + "/.local/share/pnpm",
		home + "/.npm-global/bin",
		home + "/go/bin",
		"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin", "/snap/bin",
	}, ":")
}

func engineServiceUnit(l engineLayout) string {
	return "[Unit]\nDescription=ghafk engine tick\nWants=network-online.target\nAfter=network-online.target\n\n" +
		"[Service]\nType=oneshot\nUser=" + l.user + "\nGroup=" + l.user + "\nWorkingDirectory=" + l.home + "\n" +
		"Environment=PATH=" + enginePath(l.home) + "\nEnvironment=" + configDirEnv + "=" + l.etc + "\nExecStart=" + l.bin + " tick\nTimeoutStartSec=infinity\n" +
		strings.Join(engineHardening(l.home), "\n") + "\n"
}

func engineTimerUnit(minutes int) string {
	return fmt.Sprintf("[Unit]\nDescription=run the ghafk engine every %d minutes\n\n[Timer]\nOnCalendar=*:0/%d\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", minutes, minutes)
}

func sandboxArgs(l engineLayout, command ...string) []string {
	args := []string{"systemd-run", "--wait", "--pipe", "--collect", "--quiet", "--uid=" + l.user, "--gid=" + l.user,
		"-p", "WorkingDirectory=" + l.home, "-p", "Environment=PATH=" + enginePath(l.home), "-p", "Environment=" + configDirEnv + "=" + l.etc}
	for _, p := range engineHardening(l.home) {
		args = append(args, "-p", p)
	}
	return append(args, command...)
}

func harnessesOnPath(list string) []string {
	var names []string
	for _, line := range strings.Split(list, "\n") {
		if strings.HasSuffix(line, "  on PATH") && !strings.HasSuffix(line, "not on PATH") {
			if f := strings.Fields(line); len(f) > 0 {
				names = append(names, f[0])
			}
		}
	}
	return names
}
