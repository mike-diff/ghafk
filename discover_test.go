package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryKeepsOnlyOwnedSourceRepositoriesWithTheWorkflowFile(t *testing.T) {
	out := strings.Join([]string{
		"me/app\tfalse\tfalse\ttrue",
		"me/fork-of-ghafk\ttrue\tfalse\ttrue",
		"me/retired\tfalse\ttrue\ttrue",
		"me/notes\tfalse\tfalse\tfalse",
	}, "\n")
	if got := parseDiscovery(out); !reflect.DeepEqual(got, []string{"me/app"}) {
		t.Fatalf("discovered %v; a fork, an archived repository or one without the file must not be worked", got)
	}
}

func TestDiscoveryLeavesRegisteredAndSkippedRepositoriesAlone(t *testing.T) {
	seen := map[string]bool{"me/shift": true}
	got := newRepos([]string{"Me/Shift", "me/Private", "me/new"}, seen, []string{"ME/private"})
	if !reflect.DeepEqual(got, []string{"me/new"}) {
		t.Fatalf("new repositories = %v; a registered one would be cloned twice, a skipped one worked", got)
	}
}

func TestEngineReposClonesADiscoveredRepositoryOnlyForATick(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "api graphql") {
			return "me/app\tfalse\tfalse\ttrue", nil
		}
		return "", nil
	})
	want := []target{{"me/app", filepath.Join(home, ".ghafk", "clones", "me", "app")}}

	got, err := engineRepos(home, nil, nil, false)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("status targets = %v, %v; want %v", got, err, want)
	}
	if called(*calls, "repo clone") {
		t.Fatal("status cloned a repository")
	}

	if _, err := engineRepos(home, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if !called(*calls, "repo clone me/app "+want[0].path) {
		t.Fatalf("the tick did not clone the discovered repository: %v", *calls)
	}
}

func TestListedRepositoriesAreWorkedEvenWhenDiscoveryFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "api graphql") {
			return "", errors.New("network down")
		}
		return "", nil
	})
	got, err := engineRepos(home, nil, []string{"org/tool", "org/tool"}, false)
	want := []target{{"org/tool", filepath.Join(home, ".ghafk", "clones", "org", "tool")}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, %v; want the organization repository from the repo line once", got, err)
	}
}
