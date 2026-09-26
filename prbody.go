package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

const prStatePrefix = "<!-- ghafk:pr "

type prState struct {
	Checks     string `json:"checks,omitempty"`
	ChecksSha  string `json:"checksSha,omitempty"`
	Verdict    string `json:"verdict,omitempty"`
	VerdictSha string `json:"verdictSha,omitempty"`
	Repairs    int    `json:"repairs,omitempty"`
}

func parsePRState(body string) (prState, bool) {
	lines := strings.Split(strings.TrimRight(body, " \t\r\n"), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(line, prStatePrefix) || !strings.HasSuffix(line, " -->") {
		return prState{}, false
	}
	var st prState
	if err := json.Unmarshal([]byte(unescapeComment(strings.TrimSuffix(strings.TrimPrefix(line, prStatePrefix), " -->"))), &st); err != nil {
		return prState{}, false
	}
	return st, true
}

func summaryLines(output string) string {
	lines := strings.Split(strings.TrimRight(output, " \t\r\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	if end-start > 5 {
		start = end - 5
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func bodySummary(body string) string {
	return strings.TrimSpace(strings.Join(sectionLines(body, "## ", "Summary"), "\n"))
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func prStatusRow(step, result, sha string) string {
	at := ""
	if sha != "" {
		at = shortSha(sha)
	}
	return "| " + step + " | " + result + " | " + at + " |"
}

func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := 0
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	return strings.Join(lines[start:end], "\n")
}

func renderPRBody(n int, summary, stat string, st prState) string {
	data, _ := json.Marshal(st)
	var b strings.Builder
	b.WriteString("Closes #" + strconv.Itoa(n) + "\n\n")
	b.WriteString("## Summary\n\n" + neutralize(scrubEmDashes(strings.TrimSpace(summary))) + "\n\n")
	b.WriteString("## Changes\n\n```\n" + trimBlankLines(stat) + "\n```\n\n")
	b.WriteString("## Status\n\n| Step | Result | At |\n|---|---|---|\n")
	b.WriteString(prStatusRow("Checks", st.Checks, st.ChecksSha) + "\n")
	b.WriteString(prStatusRow("Judge", st.Verdict, st.VerdictSha) + "\n")
	b.WriteString("| Repairs | " + strconv.Itoa(st.Repairs) + " |  |\n")
	b.WriteString("\n" + prStatePrefix + escapeComment(string(data)) + " -->")
	return b.String()
}

func putPRBody(repo, prNum, work, def string, n int, summary string, st prState, current string) error {
	if summary == "" {
		summary = bodySummary(current)
	}
	stat, err := run(work, "git", "diff", "--stat", "origin/"+def+"...HEAD")
	if err != nil {
		return err
	}
	body := renderPRBody(n, summary, stat, st)
	if body == current {
		return nil
	}
	_, err = gh(repo, "pr", "edit", prNum, "--body", body)
	return err
}
