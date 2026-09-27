//go:build linux

package main

import (
	"fmt"
	"os"
	"testing"
)

func TestSystemctlFindsTheUserManagerAfterSudo(t *testing.T) {
	dir := fmt.Sprintf("/run/user/%d", os.Getuid())
	if _, err := os.Stat(dir); err != nil {
		t.Skip("this user has no runtime directory")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	useUserRuntimeDir()
	if got := os.Getenv("XDG_RUNTIME_DIR"); got != dir {
		t.Fatalf("XDG_RUNTIME_DIR = %q, want %q; systemctl --user fails in a sudo -i shell", got, dir)
	}
}
