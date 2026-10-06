package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type managedPI struct {
	program string
	modules string
	node    string
}

var managedPIVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func managedPIProgram(bin, home string) (managedPI, error) {
	home = evalPath(home)
	if evalPath(bin) != filepath.Join(home, ".pi", "agent", "bin", "pi") {
		return managedPI{}, nil
	}
	fail := func(reason string) (managedPI, error) {
		return managedPI{}, fmt.Errorf("the managed Pi installation is unsafe or unsupported: %s; repair the installation or use a code-only installation outside ~/.pi", reason)
	}
	root := filepath.Join(home, ".pi", "agent", "install")
	if evalPath(root) != root {
		return fail("the install directory resolves through a link")
	}
	data, err := readSmallRegularFile(filepath.Join(root, "managed-install.json"), 4096)
	if err != nil {
		return fail("managed-install.json is missing or is not a small regular file")
	}
	var marker struct {
		Kind          string `json:"kind"`
		SchemaVersion int    `json:"schemaVersion"`
		Layout        string `json:"layout"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Kind != "pi-managed-install" || marker.SchemaVersion != 1 || marker.Layout != "releases-v1" {
		return fail("managed-install.json does not describe a releases-v1 installation")
	}
	data, err = readSmallRegularFile(filepath.Join(root, "current-version"), 128)
	if err != nil || !managedPIVersion.MatchString(strings.TrimSpace(string(data))) {
		return fail("current-version is not a small regular file with a release version")
	}
	version := strings.TrimSpace(string(data))
	modules := filepath.Join(root, "releases", version, "node_modules")
	info, err := os.Lstat(modules)
	if err != nil || !info.IsDir() || evalPath(modules) != modules {
		return fail("the selected node_modules directory is missing or resolves through a link")
	}
	if err := validatePIRuntime(modules); err != nil {
		return fail(err.Error())
	}
	pkg := filepath.Join(modules, "@earendil-works", "pi-coding-agent")
	data, err = readSmallRegularFile(filepath.Join(pkg, "package.json"), 64<<10)
	if err != nil {
		return fail("the Pi package manifest is missing or is not a small regular file")
	}
	var manifest struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Bin     map[string]string `json:"bin"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Name != "@earendil-works/pi-coding-agent" || manifest.Version != version {
		return fail("the Pi package does not match the selected release")
	}
	rel := manifest.Bin["pi"]
	if !filepath.IsLocal(rel) {
		return fail("the Pi entrypoint is not a path inside its package")
	}
	entry, err := filepath.EvalSymlinks(filepath.Join(modules, ".bin", "pi"))
	if err != nil || entry != evalPath(filepath.Join(pkg, rel)) || !underTree(entry, pkg) {
		return fail("the Pi executable does not match its package entrypoint")
	}
	info, err = os.Stat(entry)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fail("the Pi entrypoint is not an executable regular file")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	node, err := exec.LookPath(filepath.Join(dataHome, "pi-node", "current", "bin", "node"))
	if err != nil {
		node, err = exec.LookPath("node")
	}
	if err != nil {
		return fail("Node.js is not on PATH or in Pi's managed node directory")
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		return fail("the Node.js executable cannot be resolved")
	}
	return managedPI{program: entry, modules: modules, node: node}, nil
}

func validatePIRuntime(modules string) error {
	root, err := os.OpenRoot(modules)
	if err != nil {
		return fmt.Errorf("the selected runtime cannot be opened")
	}
	defer root.Close()
	entries := 0
	return fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("the selected runtime cannot be inspected")
		}
		entries++
		if entries > 100000 {
			return fmt.Errorf("the selected runtime has too many entries")
		}
		info, err := root.Stat(path)
		if err != nil {
			return fmt.Errorf("the selected runtime has a broken or escaping link")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("the selected runtime has a non-regular file")
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Nlink != 1 {
			return fmt.Errorf("the selected runtime has a hard-linked file")
		}
		return nil
	})
}
