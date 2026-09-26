package main

import "fmt"

const serviceUnit = `[Unit]
Description=ghafk tick

[Service]
Type=oneshot
TimeoutStartSec=4h
Environment=PATH=%s
ExecStart=%s tick
`

const timerUnit = `[Unit]
Description=run ghafk tick every %d minutes

[Timer]
OnCalendar=*:0/%d
Persistent=true

[Install]
WantedBy=timers.target
`

func timerFor(minutes int) string {
	return fmt.Sprintf(timerUnit, minutes, minutes)
}

func serviceFor(exe, path string) string {
	return fmt.Sprintf(serviceUnit, path, exe)
}
