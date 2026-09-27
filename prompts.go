package main

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed prompts/*.md
var builtinPrompts embed.FS

func rolePrompt(role string) string {
	return strings.TrimSpace(promptFile(role)) + "\n\n" + strings.TrimSpace(promptFile("common"))
}

func promptFile(name string) string {
	if dir, err := configDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(dir, "prompts", name+".md")); err == nil {
			return string(data)
		}
	}
	data, _ := builtinPrompts.ReadFile("prompts/" + name + ".md")
	return string(data)
}

func workerPrompt(wf workflow) string {
	checks := "`" + wf.checks + "`"
	if wf.checks == "" {
		checks = "the tests of this repository"
	}
	return strings.ReplaceAll(rolePrompt("worker"), "{checks}", checks)
}
