package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mike-diff/ghafk/internal/harness"
)

const harnessLabelDescription = "ghafk: run this issue with this worker harness"

func initRepo(path string) error {
	root, err := run(path, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s: not a git repository", path)
	}
	name, err := ghOwner(root, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return err
	}
	if _, err := ghOwner(root, "label", "create", "agent", "--force"); err != nil {
		return err
	}
	if err := initHarnessLabels(root); err != nil {
		return err
	}
	workflow := filepath.Join(root, ".ghafk", "WORKFLOW.md")
	note := "kept existing " + workflow
	if _, err := os.Stat(workflow); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Join(root, ".ghafk"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(workflow, []byte(workflowText(guessChecks(root))), 0o644); err != nil {
			return err
		}
		note = "wrote " + workflow
	} else if err != nil {
		return err
	}
	file, err := reposFile()
	if err != nil {
		return err
	}
	if err := addRepo(file, root); err != nil {
		return err
	}
	fmt.Printf("ghafk: registered %s (%s)\n", root, name)
	fmt.Println("ghafk: " + note)
	fmt.Println("ghafk: review and commit the workflow file; ghafk never commits or pushes")
	fmt.Println("ghafk: after you push it to the default branch, an engine that runs as another user finds the repository on its next tick")
	if wf, err := os.ReadFile(workflow); err == nil && !strings.Contains(string(wf), "\nchecks:") {
		fmt.Println("ghafk: the workflow file has no checks line; add one, or every pull request parks before merge")
	}
	if henv, err := loadHarnessEnv(); err == nil && henv.Default == "" {
		fmt.Println("ghafk: no default harness is set; run `ghafk harness default <profile> <model>` or add a worker line to the workflow file")
	}
	return nil
}

func plannedHarnessLabels(profiles map[string]harness.Profile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var planned []string
	for _, name := range names {
		if harnessOnPath(profiles[name].Command) {
			planned = append(planned, "harness:"+name)
		}
	}
	return planned
}

func initHarnessLabels(root string) error {
	henv, err := loadHarnessEnv()
	if err != nil {
		return err
	}
	planned := plannedHarnessLabels(henv.Profiles)
	if len(planned) == 0 {
		return nil
	}
	out, err := ghOwner(root, "label", "list", "--json", "name")
	if err != nil {
		return err
	}
	var existing []label
	if err := json.Unmarshal([]byte(out), &existing); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, l := range existing {
		have[l.Name] = true
	}
	for _, name := range planned {
		if have[name] {
			continue
		}
		if _, err := ghOwner(root, "label", "create", name, "--color", "1D76DB", "--description", harnessLabelDescription); err != nil {
			return err
		}
	}
	return nil
}

func removeRepo(path string) error {
	root, err := run(path, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		if root, err = filepath.Abs(path); err != nil {
			return err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if strings.HasPrefix(root, filepath.Join(home, ".ghafk", "clones")+string(filepath.Separator)) {
		return fmt.Errorf("%s was found on GitHub, not registered; delete .ghafk/WORKFLOW.md from its default branch, or add a skip line to ~/.ghafk/config", root)
	}
	file, err := reposFile()
	if err != nil {
		return err
	}
	if err := dropRepoLine(file, root); err != nil {
		return err
	}
	fmt.Printf("ghafk: removed %s\n", root)
	listInFlight(root)
	return nil
}

func reposFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ghafk", "repos"), nil
}

func guessChecks(root string) string {
	for _, guess := range []struct{ file, checks string }{
		{"go.mod", `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -count=1 ./...`},
		{"pnpm-lock.yaml", `pnpm install --frozen-lockfile && pnpm test`},
		{"yarn.lock", `yarn install --frozen-lockfile && yarn test`},
		{"package-lock.json", `npm ci && npm test`},
		{"package.json", `npm install && npm test`},
		{"Cargo.toml", `cargo test`},
		{"pyproject.toml", `pytest`},
	} {
		if _, err := os.Stat(filepath.Join(root, guess.file)); err == nil {
			return guess.checks
		}
	}
	return ""
}

func workflowText(checks string) string {
	text := "---\nlabel: agent\n"
	if checks != "" {
		text += "checks: " + checks + "\n"
	}
	return text + "---\n"
}

func addRepo(file, repo string) error {
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == repo {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(file, []byte(text+repo+"\n"), 0o644)
}

func dropRepoLine(file, repo string) error {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != repo {
			kept = append(kept, line)
		}
	}
	if out := strings.Join(kept, "\n"); out != string(data) {
		return os.WriteFile(file, []byte(out), 0o644)
	}
	return nil
}

func listInFlight(root string) {
	out, err := ghOwner(root, "pr", "list", "--state", "open", "--json", "number,headRefName")
	var prs []pr
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &prs); jerr != nil {
			err = jerr
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: %v\n", err)
		return
	}
	for _, p := range prs {
		if strings.HasPrefix(p.HeadRefName, "agent/") {
			fmt.Printf("left in flight: PR #%d (%s)\n", p.Number, p.HeadRefName)
		}
	}
}
