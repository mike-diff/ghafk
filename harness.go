package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mike-diff/ghafk/internal/harness"
)

func loadHarnessEnv() (harness.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return harness.Env{}, err
	}
	return harness.LoadEnv(filepath.Join(home, ".ghafk"))
}

func workerForLabels(wf workflow, labels []label) (harness.Role, error) {
	var harnesses, models []string
	for _, l := range labels {
		if v, ok := strings.CutPrefix(l.Name, "harness:"); ok {
			harnesses = append(harnesses, v)
		}
		if v, ok := strings.CutPrefix(l.Name, "model:"); ok {
			models = append(models, v)
		}
	}
	if len(harnesses) == 0 && len(models) == 0 {
		return wf.worker, nil
	}
	if len(harnesses) > 1 {
		return harness.Role{}, fmt.Errorf("%d %q labels are set; exactly one is allowed", len(harnesses), "harness:")
	}
	if len(models) > 1 {
		return harness.Role{}, fmt.Errorf("%d %q labels are set; exactly one is allowed", len(models), "model:")
	}
	if wf.workerProfile == "" && wf.workerModel == "" {
		if len(models) > 0 {
			return harness.Role{}, fmt.Errorf("label %q cannot override the raw worker command of this repository", "model:"+models[0])
		}
		return harness.Role{}, fmt.Errorf("label %q names a harness, but the repository's worker is a raw command with no model; add a \"model:<id>\" label", "harness:"+harnesses[0])
	}
	profile, model := wf.workerProfile, wf.workerModel
	if len(harnesses) > 0 {
		profile = harnesses[0]
	}
	if len(models) > 0 {
		model = models[0]
	}
	p, known := wf.profiles[profile]
	if !known {
		return harness.Role{}, fmt.Errorf("label %q names no configured harness profile", "harness:"+profile)
	}
	if !harness.ValidModel.MatchString(model) {
		return harness.Role{}, fmt.Errorf("label %q names an invalid model %q", "model:"+model, model)
	}
	if len(models) > 0 && len(p.Allow) > 0 && !slices.Contains(p.Allow, model) {
		return harness.Role{}, fmt.Errorf("label %q names model %q, which harness %q does not allow", "model:"+model, model, profile)
	}
	return harness.ResolveRole(profile+" "+model, wf.profiles)
}
