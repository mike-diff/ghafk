package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func managedPIFixture(t *testing.T) (home, launcher string) {
	t.Helper()
	home = t.TempDir()
	for _, dir := range []string{".pi/agent/bin", ".pi/agent/install", ".local/bin", "tools/bin"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".pi/agent/bin/pi":                       "#!/bin/sh\necho launcher-ran > " + strconv.Quote(filepath.Join(home, "launcher-ran")) + "\nexit 99\n",
		".pi/agent/install/managed-install.json": `{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`,
		".pi/agent/install/current-version":      "1.0.3\n",
		".pi/agent/auth.json":                    `{"key":"fixture-pi-login"}`,
		"tools/bin/node":                         "#!/bin/sh\nexec /bin/sh \"$@\"\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(rel)), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	launcher = filepath.Join(home, ".local", "bin", "pi")
	if err := os.Symlink("../../.pi/agent/bin/pi", launcher); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(home, "tools", "bin")+string(os.PathListSeparator)+filepath.Join(home, ".local", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	managedPIRelease(t, home, "1.0.3", "printf '%s\\n' 1.0.3\n")
	return home, launcher
}

func managedPIRelease(t *testing.T, home, version, script string) {
	t.Helper()
	modules := filepath.Join(home, ".pi", "agent", "install", "releases", version, "node_modules")
	pkg := filepath.Join(modules, "@earendil-works", "pi-coding-agent")
	for _, dir := range []string{filepath.Join(pkg, "dist"), filepath.Join(modules, ".bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := fmt.Sprintf(`{"name":"@earendil-works/pi-coding-agent","version":%s,"bin":{"pi":"dist/cli.js"}}`, strconv.Quote(version))
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "dist", "cli.js"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../@earendil-works/pi-coding-agent/dist/cli.js", filepath.Join(modules, ".bin", "pi")); err != nil {
		t.Fatal(err)
	}
}

func managedPISpec(t *testing.T, home, command, profile string) (sandboxSpec, error) {
	t.Helper()
	role := harness.Role{Command: command, Profile: profile}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, command: command, role: &role, timeout: time.Minute})
	t.Cleanup(func() {
		removeTree(spec.runDir)
		removeTree(spec.sockDir)
	})
	return spec, err
}

func TestManagedPIPinsTheSelectedReleaseAndPreservesArguments(t *testing.T) {
	home, launcher := managedPIFixture(t)
	for _, command := range []string{"pi --version", strconv.Quote(launcher) + " -p --model provider/model:max"} {
		spec, err := managedPISpec(t, home, command, "pi")
		if err != nil {
			t.Fatal(err)
		}
		modules := evalPath(filepath.Join(home, ".pi", "agent", "install", "releases", "1.0.3", "node_modules"))
		entry := filepath.Join(modules, "@earendil-works", "pi-coding-agent", "dist", "cli.js")
		if spec.programs[1] != entry || firstWord(spec.command) != spec.programs[0] {
			t.Fatalf("the launcher must be bypassed: programs=%v command=%q", spec.programs, spec.command)
		}
		if spec.command != strconv.Quote(spec.programs[0])+" "+strconv.Quote(entry)+command[len(strings.Fields(command)[0]):] {
			t.Fatalf("the harness arguments changed: %q -> %q", command, spec.command)
		}
		if !slices.Contains(spec.programDirs, modules) {
			t.Fatalf("the selected dependency tree is not available: %v", spec.programDirs)
		}
		for _, forbidden := range []string{filepath.Join(home, ".pi"), filepath.Join(home, ".pi", "agent"), filepath.Join(home, ".pi", "agent", "bin"), filepath.Join(home, ".pi", "agent", "install")} {
			if slices.Contains(spec.programDirs, evalPath(forbidden)) {
				t.Fatalf("credential or installer tree exposed: %v", spec.programDirs)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, "launcher-ran")); !os.IsNotExist(err) {
		t.Fatalf("the launcher ran on the host: %v", err)
	}
}

func TestManagedPIRejectsInvalidMetadataAndEntrypoints(t *testing.T) {
	cases := []struct {
		name, rel, body string
	}{
		{"marker", "managed-install.json", `{"kind":"other","schemaVersion":1,"layout":"releases-v1"}`},
		{"schema", "managed-install.json", `{"kind":"pi-managed-install","schemaVersion":2,"layout":"releases-v1"}`},
		{"traversal", "current-version", "../../auth.json"},
		{"missing release", "current-version", "9.9.9"},
		{"manifest version", "releases/1.0.3/node_modules/@earendil-works/pi-coding-agent/package.json", `{"name":"@earendil-works/pi-coding-agent","version":"9.9.9","bin":{"pi":"dist/cli.js"}}`},
		{"manifest entry", "releases/1.0.3/node_modules/@earendil-works/pi-coding-agent/package.json", `{"name":"@earendil-works/pi-coding-agent","version":"1.0.3","bin":{"pi":"../../../../auth.json"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := managedPIFixture(t)
			if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "install", filepath.FromSlash(tc.rel)), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := managedPISpec(t, home, "pi --version", "pi"); err == nil {
				t.Fatal("an invalid managed install was accepted")
			}
		})
	}
}

func TestManagedPIRejectsLinksToCredentialOrRepositoryTrees(t *testing.T) {
	for _, rel := range []string{"current-version", "releases/1.0.3/node_modules", "releases/1.0.3/node_modules/leak", "releases/1.0.3/node_modules/.bin/pi"} {
		t.Run(rel, func(t *testing.T) {
			home, _ := managedPIFixture(t)
			path := filepath.Join(home, ".pi", "agent", "install", filepath.FromSlash(rel))
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, ".pi", "agent", "auth.json")
			if strings.HasSuffix(rel, "node_modules") {
				target = t.TempDir()
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := managedPISpec(t, home, "pi --version", "pi"); err == nil {
				t.Fatalf("a link outside the selected runtime was accepted: %s", rel)
			}
		})
	}
}

func TestManagedPIRejectsHardLinkedRuntimeCredentials(t *testing.T) {
	home, _ := managedPIFixture(t)
	leak := filepath.Join(home, ".pi", "agent", "install", "releases", "1.0.3", "node_modules", "leak")
	if err := os.Link(filepath.Join(home, ".pi", "agent", "auth.json"), leak); err != nil {
		t.Fatal(err)
	}
	if _, err := managedPISpec(t, home, "pi --version", "pi"); err == nil {
		t.Fatal("a credential hard link was exposed with the runtime")
	}
}

func TestManagedPIExceptionDoesNotApplyToRawCommandsOrOtherProfiles(t *testing.T) {
	for _, profile := range []string{"", "custom-pi"} {
		t.Run(profile, func(t *testing.T) {
			home, _ := managedPIFixture(t)
			if _, err := managedPISpec(t, home, "pi --version", profile); err == nil || !strings.Contains(err.Error(), "holds logins or keys") {
				t.Fatalf("a non-Pi profile gained access to the credential tree: %v", err)
			}
		})
	}
}

func TestManagedPIRunKeepsHostStateHiddenAndCodeReadOnly(t *testing.T) {
	needSandbox(t)
	home, _ := managedPIFixture(t)
	modules := filepath.Join(home, ".pi", "agent", "install", "releases", "1.0.3", "node_modules")
	entry := filepath.Join(modules, "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	script := "#!/bin/sh\n" + `
if [ "${1-}" = "--version" ]; then echo 1.0.3; exit; fi
set -eu
grep -q fixture-pi-login "$HOME/.pi/agent/auth.json"
test ! -e "$HOME/.pi/agent/settings.json"
test ! -e "$HOME/.pi/agent/extensions"
test ! -e "$HOME/.pi/agent/bin/pi"
test ! -e "$HOME/.pi/agent/install/current-version"
test ! -e "$HOME/.pi/agent/install/managed-install.json"
test ! -e "$HOME/.ghafk/env"
if printf tamper >> "$0" 2>/dev/null; then exit 1; fi
echo managed-runtime-safe
`
	if err := os.WriteFile(entry, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".pi/agent/settings.json", ".pi/agent/extensions/private.ts", ".ghafk/env"} {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("host-only-marker"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := "pi --audit"
	role := harness.Role{Command: command, Profile: "pi"}
	var out bytes.Buffer
	if err := runSandboxed(sandboxOpts{name: "worker", dir: t.TempDir(), home: home, role: &role, command: command, timeout: time.Minute}, &out, &out); err != nil {
		t.Fatalf("the native managed runtime failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "managed-runtime-safe") {
		t.Fatalf("isolation checks did not complete: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "launcher-ran")); !os.IsNotExist(err) {
		t.Fatalf("the original launcher was executed: %v", err)
	}
}

func TestManagedPIUsesTheInstallerNodeWithOrWithoutAPathNode(t *testing.T) {
	for _, hasPathNode := range []bool{true, false} {
		t.Run(fmt.Sprintf("PATH-node=%v", hasPathNode), func(t *testing.T) {
			home, _ := managedPIFixture(t)
			if !hasPathNode {
				t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
			}
			dataHome := filepath.Join(home, "managed-node-data")
			t.Setenv("XDG_DATA_HOME", dataHome)
			node := filepath.Join(dataHome, "pi-node", "current", "bin", "node")
			if err := os.MkdirAll(filepath.Dir(node), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(node, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			spec, err := managedPISpec(t, home, "pi --version", "pi")
			if err != nil {
				t.Fatal(err)
			}
			if spec.programs[0] != evalPath(node) || firstWord(spec.command) != evalPath(node) || !slices.Contains(filepath.SplitList(spec.path), filepath.Dir(evalPath(node))) {
				t.Fatalf("the installer's Node was not selected: programs=%v command=%q PATH=%s", spec.programs, spec.command, spec.path)
			}
		})
	}
}

func TestManagedPIRejectsAReleaseInsideAWorkedRepository(t *testing.T) {
	home, _ := managedPIFixture(t)
	role := harness.Role{Command: "pi --version", Profile: "pi"}
	spec, err := buildSandbox(sandboxOpts{name: "worker", home: home, dir: t.TempDir(), repo: filepath.Join(home, ".pi", "agent", "install"), command: role.Command, role: &role})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err == nil || !strings.Contains(err.Error(), "which ghafk works on") {
		t.Fatalf("a managed runtime in a worked repository was accepted: %v", err)
	}
}

func TestManagedPIMacOSPolicyDoesNotGrantTheCredentialTree(t *testing.T) {
	home, _ := managedPIFixture(t)
	spec, err := managedPISpec(t, home, "pi --version", "pi")
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfile(seatbeltPaths(spec), "18080")
	for _, rel := range []string{".pi", ".pi/agent", ".pi/agent/install", ".pi/agent/install/releases"} {
		if strings.Contains(profile, "(subpath "+strconv.Quote(evalPath(filepath.Join(home, filepath.FromSlash(rel))))+")") {
			t.Fatalf("macOS grants more than the selected runtime: %s", profile)
		}
	}
	if !strings.Contains(profile, "(subpath "+strconv.Quote(spec.piRuntime)+")") {
		t.Fatal("macOS cannot read the selected dependency tree")
	}
}

func TestManagedPIUpdateDoesNotRetargetAPreparedSandbox(t *testing.T) {
	needSandbox(t)
	home, _ := managedPIFixture(t)
	old, err := managedPISpec(t, home, "pi --version", "pi")
	if err != nil {
		t.Fatal(err)
	}
	managedPIRelease(t, home, "1.0.4", "printf '%s\\n' 1.0.4\n")
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "install", "current-version"), []byte("1.0.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, err := managedPISpec(t, home, "pi --version", "pi")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		spec sandboxSpec
		want string
	}{{old, "1.0.3"}, {next, "1.0.4"}} {
		proxy := &egressProxy{}
		if err := proxyStart(proxy, tc.spec); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err := sandboxLaunch(sandboxOpts{name: "worker", dir: tc.spec.workDir, timeout: time.Minute}, tc.spec, proxy, tc.spec.command, &out, &out)
		proxy.close()
		if err != nil || strings.TrimSpace(out.String()) != tc.want {
			t.Fatalf("prepared sandbox used the wrong version: want=%s output=%q err=%v", tc.want, out.String(), err)
		}
	}
}
