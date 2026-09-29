package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheGoToolchainRunsInsideTheSandbox(t *testing.T) {
	needSandbox(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() { println(\"go-in-sandbox\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err := runSandboxed(sandboxOpts{name: "checks", dir: dir, command: "go mod init smoke >/dev/null 2>&1; go build -o app . && ./app 2>&1 && test -d \"$GOCACHE\" && echo GOCACHE-OK", timeout: 5 * time.Minute, home: t.TempDir()}, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	combined := out.String() + errb.String()
	if !strings.Contains(combined, "go-in-sandbox") || !strings.Contains(combined, "GOCACHE-OK") {
		t.Fatalf("go did not build and run in the sandbox:\n%s", combined)
	}
}
