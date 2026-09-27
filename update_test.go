package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateInstallsMainPastTheModuleProxy(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	fakeGo := "#!/bin/sh\nif [ \"$1\" = env ]; then printf '%s\\n' '" + dir + "' ''; exit 0; fi\n" +
		"printf '%s GOPROXY=%s\\n' \"$*\" \"$GOPROXY\" >> '" + record + "'\nprintf '#!/bin/sh\\n' > '" + filepath.Join(dir, "ghafk") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := update(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(record)
	if !strings.Contains(string(got), "install github.com/mike-diff/ghafk@main GOPROXY=direct") {
		t.Fatalf("go was called as %q; @latest through the proxy can return a stale version for a while after a merge", got)
	}
	if bin, err := installedBinary(); err != nil || bin != filepath.Join(dir, "ghafk") {
		t.Fatalf("installed binary = %q, %v", bin, err)
	}
}
