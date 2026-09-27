package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const discoverQuery = `query($endCursor: String) {
  viewer {
    repositories(first: 100, after: $endCursor, ownerAffiliations: OWNER) {
      pageInfo { hasNextPage endCursor }
      nodes { nameWithOwner isFork isArchived object(expression: "HEAD:.ghafk/WORKFLOW.md") { __typename } }
    }
  }
}`

const discoverJQ = `.data.viewer.repositories.nodes[] | "\(.nameWithOwner)\t\(.isFork)\t\(.isArchived)\t\(.object != null)"`

type target struct {
	name string
	path string
}

func engineRepos(home string, skip []string, cloneNew bool) ([]target, error) {
	file, err := reposFile()
	if err != nil {
		return nil, err
	}
	paths, err := readRepos(file)
	if err != nil {
		return nil, err
	}
	var targets []target
	seen := map[string]bool{}
	for _, path := range paths {
		name, err := ghOwner(path, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
			continue
		}
		seen[strings.ToLower(name)] = true
		targets = append(targets, target{name, path})
	}
	out, err := ghOwner(home, "api", "graphql", "--paginate", "-f", "query="+discoverQuery, "--jq", discoverJQ)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: repository discovery failed: %v\n", err)
		return targets, nil
	}
	for _, name := range newRepos(parseDiscovery(out), seen, skip) {
		path := filepath.Join(home, ".ghafk", "clones", name)
		if _, err := os.Stat(path); os.IsNotExist(err) && cloneNew {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return targets, err
			}
			if _, err := ghOwner(home, "repo", "clone", name, path); err != nil {
				fmt.Fprintf(os.Stderr, "%s: clone failed: %v\n", name, err)
				continue
			}
			fmt.Printf("ghafk: cloned %s into %s\n", name, path)
		}
		targets = append(targets, target{name, path})
	}
	return targets, nil
}

func parseDiscovery(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 4 && f[1] == "false" && f[2] == "false" && f[3] == "true" {
			names = append(names, f[0])
		}
	}
	return names
}

func newRepos(found []string, seen map[string]bool, skip []string) []string {
	var names []string
	for _, name := range found {
		if seen[strings.ToLower(name)] || skipped(name, skip) {
			continue
		}
		names = append(names, name)
	}
	return names
}

func skipped(name string, skip []string) bool {
	for _, s := range skip {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}
