package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

type workflow struct {
	worker        harness.Role
	workerProfile string
	workerModel   string
	profiles      map[string]harness.Profile
	groomer       harness.Role
	judge         harness.Role
	reconciler    harness.Role
	label         string
	checks        string
	body          string
	timeout       time.Duration
}

func parseWorkflow(data string, henv harness.Env) (workflow, error) {
	wf := workflow{label: "agent", timeout: 30 * time.Minute}
	lines := strings.Split(data, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return wf, fmt.Errorf("WORKFLOW.md: missing front matter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return wf, fmt.Errorf("WORKFLOW.md: unterminated front matter")
	}
	var worker, groomer, judge, reconciler string
	for _, line := range lines[1:end] {
		key, value, _ := strings.Cut(line, ":")
		switch strings.TrimSpace(key) {
		case "worker":
			worker = strings.TrimSpace(value)
		case "groomer":
			groomer = strings.TrimSpace(value)
		case "judge":
			judge = strings.TrimSpace(value)
		case "reconciler":
			reconciler = strings.TrimSpace(value)
		case "label":
			wf.label = strings.TrimSpace(value)
		case "checks":
			wf.checks = strings.TrimSpace(value)
		case "timeout":
			minutes, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || minutes <= 0 {
				return wf, fmt.Errorf("WORKFLOW.md: bad timeout")
			}
			wf.timeout = time.Duration(minutes) * time.Minute
		}
	}
	if worker == "" {
		worker = henv.Default
	}
	if worker == "" {
		return wf, fmt.Errorf("WORKFLOW.md: no worker")
	}
	var err error
	if wf.worker, err = harness.ResolveRole(worker, henv.Profiles); err != nil {
		return wf, err
	}
	wf.profiles = henv.Profiles
	if fields := strings.Fields(worker); len(fields) > 0 {
		if _, ok := henv.Profiles[fields[0]]; ok {
			wf.workerProfile = fields[0]
			wf.workerModel = strings.TrimSpace(worker[len(fields[0]):])
		}
	}
	wf.groomer = wf.worker
	if groomer != "" {
		if wf.groomer, err = harness.ResolveRole(groomer, henv.Profiles); err != nil {
			return wf, err
		}
	}
	wf.judge = wf.worker
	if judge != "" {
		if wf.judge, err = harness.ResolveRole(judge, henv.Profiles); err != nil {
			return wf, err
		}
	}
	wf.reconciler = wf.groomer
	if reconciler != "" {
		if wf.reconciler, err = harness.ResolveRole(reconciler, henv.Profiles); err != nil {
			return wf, err
		}
	}
	wf.body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return wf, nil
}

func loadWorkflow(repo string, henv harness.Env) (workflow, error) {
	data, err := os.ReadFile(filepath.Join(repo, ".ghafk", "WORKFLOW.md"))
	if err != nil {
		return workflow{}, err
	}
	return parseWorkflow(string(data), henv)
}
