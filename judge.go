package main

import (
	"bytes"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

const diffLineCap = 4000

func judgeContract(is issue, login string) string {
	if text := contractText(is.Comments, login); text != "" {
		return text
	}
	return "# Issue #" + strconv.Itoa(is.Number) + ": " + is.Title + "\n\n" + strings.TrimSpace(is.Body)
}

func parseItems(answer string) map[int]cardItem {
	items := map[int]cardItem{}
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimLeft(strings.Trim(line, "`*# \t"), "-*+ \t")
		var status string
		switch {
		case strings.HasPrefix(line, "pass "):
			status = "pass"
		case strings.HasPrefix(line, "fail "):
			status = "fail"
		case strings.HasPrefix(line, "manual "):
			status = "manual"
		default:
			continue
		}
		rest := strings.TrimSpace(line[len(status)+1:])
		digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
		reason := strings.TrimSpace(strings.TrimLeft(rest[digits:], ":—–- \t"))
		k, err := strconv.Atoi(rest[:digits])
		if err != nil || k < 1 {
			continue
		}
		items[k] = cardItem{Status: status, Reason: reason}
	}
	return items
}

func runJudge(base string, n int, work string, jr harness.Role, body, contract, def string, timeout time.Duration, egress []string, repo, home string) (string, int, error) {
	diff, err := run(work, "git", "diff", "origin/"+def+"...HEAD")
	if err != nil {
		return "", 0, err
	}
	prompt := joinParts(rolePrompt("judge"), body, contract, "# Diff\n"+capDiff(diff))
	var out bytes.Buffer
	if err := runSandboxed(sandboxOpts{name: "judge", dir: work, command: jr.Command, stdin: prompt, timeout: timeout, role: &jr, egress: egress, repo: repo, home: home}, &out, os.Stderr); err != nil {
		return "", 0, err
	}
	answer, tokens := reportRoleUsage(base, n, jr, redactSecrets(out.String()))
	if _, err := run(work, "git", "checkout", "--", "."); err != nil {
		return "", 0, err
	}
	if _, err := run(work, "git", "clean", "-fdq"); err != nil {
		return "", 0, err
	}
	return answerFrom(answer, "approve:", "reject:"), tokens, nil
}

func verdictHead(kind, sha string) string {
	if kind == "approve" {
		return "Approved at " + shortSha(sha)
	}
	return "Changes requested at " + shortSha(sha)
}

func renderVerdict(kind, sha string, items map[int]cardItem) string {
	head := verdictHead(kind, sha)
	if len(items) == 0 {
		return head
	}
	var keys []int
	for k := range items {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	b.WriteString(head + "\n\n| # | Result | Evidence |\n|---|---|---|")
	for _, k := range keys {
		b.WriteString("\n| " + strconv.Itoa(k) + " | " + items[k].Status + " | " + strings.ReplaceAll(neutralize(clipReason(items[k].Reason)), "|", "\\|") + " |")
	}
	return b.String()
}

func verdictCommand(prNum, prAuthor, login, kind, body string) []string {
	if prAuthor == login {
		return []string{"pr", "comment", prNum, "--body", body}
	}
	flag := "--approve"
	if kind == "reject" {
		flag = "--request-changes"
	}
	return []string{"pr", "review", prNum, flag, "--body", body}
}

func postVerdict(repo, prNum, prAuthor, login, kind, body string) error {
	_, err := gh(repo, verdictCommand(prNum, prAuthor, login, kind, body)...)
	return err
}

func capDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	if len(lines) > diffLineCap {
		lines = append(lines[:diffLineCap], "[diff truncated]")
	}
	return strings.Join(lines, "\n")
}
