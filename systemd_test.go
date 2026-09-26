package main

import (
	"errors"
	"strings"
	"testing"
)

func TestServiceCarriesTheCallersPath(t *testing.T) {
	unit := serviceFor("/bin/ghafk", "/home/me/.nvm/bin:/usr/bin")
	if !strings.Contains(unit, "Environment=PATH=/home/me/.nvm/bin:/usr/bin\n") {
		t.Fatalf("unit does not pin the caller's PATH:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/bin/ghafk tick\n") {
		t.Fatalf("unit does not run this binary:\n%s", unit)
	}
}

func TestTimerRunsOnTheWallClock(t *testing.T) {
	if !strings.Contains(timerFor(2), "OnCalendar=*:0/2\n") {
		t.Fatalf("timer is not scheduled on the wall clock, so a restart can leave it elapsed with no next run:\n%s", timerFor(2))
	}
	if strings.Contains(timerFor(2), "OnUnitActiveSec") || strings.Contains(timerFor(2), "OnBootSec") {
		t.Fatalf("timer still carries a relative trigger:\n%s", timerFor(2))
	}
}

func TestServiceHasAStartTimeout(t *testing.T) {
	if unit := serviceFor("/bin/ghafk", "/usr/bin"); !strings.Contains(unit, "TimeoutStartSec=") {
		t.Fatalf("a hung tick would block every later tick forever:\n%s", unit)
	}
}

func TestStatusShowsWhyTheSchedulerIsUnavailable(t *testing.T) {
	if got := orError("", errors.New(`exec: "systemctl": executable file not found in $PATH`)); !strings.Contains(got, "systemctl") {
		t.Fatalf("status printed %q, which hides why the timer cannot be read", got)
	}
	if got := orError("inactive", errors.New("exit status 3")); got != "inactive" {
		t.Fatalf("status printed %q, want the command output when there is some", got)
	}
}
