package main

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	parkMarker           = "<!-- ghafk:park -->"
	verdictMarker        = "<!-- ghafk:verdict -->"
	reconcileStaleMarker = "<!-- ghafk:reconcile-stale -->"
	repairMarker         = "<!-- ghafk:repair -->"
)

const evidenceLines = 20

type commentSpec struct {
	kind     string
	role     string
	number   int
	sentence string
	body     string
	footer   string
}

func commentMarker(kind string) string {
	return "<!-- ghafk:" + kind + " -->"
}

func renderComment(spec commentSpec) string {
	var b strings.Builder
	b.WriteString(commentMarker(spec.kind) + "\n> [!")
	if spec.kind == "park" || spec.kind == "question" {
		b.WriteString("IMPORTANT]\n")
	} else {
		b.WriteString("NOTE]\n")
	}
	b.WriteString("> **ghafk** · " + spec.role + " · #" + strconv.Itoa(spec.number) + "\n")
	b.WriteString("> " + strings.TrimSpace(spec.sentence) + "\n")
	if body := strings.TrimSpace(spec.body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return scrubEmDashes(strings.TrimRight(b.String(), "\n") + spec.footer)
}

func postComment(repo, num string, spec commentSpec, prior []prComment, login string) error {
	body := renderComment(spec)
	if spec.kind == "park" && parkDupe(prior, login, body) {
		return nil
	}
	_, err := gh(repo, "issue", "comment", num, "--body", body)
	return err
}

func parkDupe(prior []prComment, login, body string) bool {
	for i := len(prior) - 1; i >= 0; i-- {
		if engineWrote(prior[i], login) {
			return prior[i].Body == body
		}
	}
	return false
}

func scrubEmDashes(s string) string {
	parts := strings.Split(s, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = strings.ReplaceAll(parts[i], " — ", ", ")
		parts[i] = strings.ReplaceAll(parts[i], "—", ", ")
	}
	return strings.Join(parts, "`")
}

func detailsBlock(summary, text string) string {
	return "<details>\n<summary>" + summary + "</summary>\n\n" + fenced(strings.TrimSpace(text)) + "\n\n</details>"
}

func fenced(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + text + "\n" + fence
}

var (
	closingRef = regexp.MustCompile(`(?i)\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)(:?\s+)((?:[\w.-]+/[\w.-]+)?#\d+|https?://github\.com/[\w.-]+/[\w.-]+/(?:issues|pull)/\d+)`)
	mention    = regexp.MustCompile("@[A-Za-z0-9][A-Za-z0-9-]*")
)

func neutralize(s string) string {
	s = strings.ReplaceAll(s, "<!--", "&lt;!--")
	s = closingRef.ReplaceAllString(s, "${1}${2}`${3}`")
	var b strings.Builder
	last := 0
	for _, m := range mention.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		before, after := byte(0), byte(0)
		if start > 0 {
			before = s[start-1]
		}
		if end < len(s) {
			after = s[end]
		}
		if isWordByte(before) || before == '`' && after == '`' {
			continue
		}
		b.WriteString(s[last:start] + "`" + s[start:end] + "`")
		last = end
	}
	return b.String() + s[last:]
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func exitCodeOf(err error) (int, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	return 0, false
}

func timeoutSentence(role string, limit time.Duration) string {
	return "The " + role + " ran longer than the " + strconv.Itoa(int(limit.Minutes())) + " minute limit and was stopped."
}

func runFailure(role string, err error, limit time.Duration) (string, string) {
	code, exited := exitCodeOf(err)
	switch {
	case exited && code >= 0:
		return "The " + role + " exited with code " + strconv.Itoa(code) + ".", ""
	case exited && strings.Contains(err.Error(), "signal: killed"):
		return timeoutSentence(role, limit), ""
	default:
		return "The " + role + " failed.", detailsBlock("Error", err.Error())
	}
}

func failureEvidence(command string, err error, output string, limit time.Duration) string {
	code, exited := exitCodeOf(err)
	first := "The command `" + command + "` failed."
	switch {
	case exited && code >= 0:
		first = "The command `" + command + "` exited with code " + strconv.Itoa(code) + "."
	case exited:
		first = timeoutSentence("checks", limit)
	}
	return first + "\n\n" + evidenceDetails(output)
}

func evidenceDetails(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var matched []string
	for _, line := range lines {
		if len(matched) == evidenceLines {
			break
		}
		if evidenceLine(line) {
			matched = append(matched, line)
		}
	}
	if len(matched) == 0 {
		matched = lines
		if len(matched) > evidenceLines {
			matched = matched[:evidenceLines]
		}
	}
	tail := lines
	if len(tail) > evidenceLines {
		tail = tail[len(tail)-evidenceLines:]
	}
	return "<details>\n<summary>Evidence</summary>\n\n" + fenced(strings.Join(matched, "\n")) + "\n\n" + fenced(strings.Join(tail, "\n")) + "\n\n</details>"
}

func evidenceLine(line string) bool {
	for _, needle := range []string{"FAIL", "error", "panic", "---"} {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func parseQuestion(text string) (string, []string) {
	var pre []string
	var options []string
	for _, line := range strings.Split(text, "\n") {
		if opt, ok := optionLine(line); ok {
			options = append(options, opt)
			continue
		}
		if len(options) == 0 {
			pre = append(pre, line)
		}
	}
	return strings.TrimSpace(strings.Join(pre, "\n")), options
}

func optionLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	digits := len(s) - len(strings.TrimLeft(s, "0123456789"))
	if digits == 0 {
		return "", false
	}
	rest := s[digits:]
	if !strings.HasPrefix(rest, ".") && !strings.HasPrefix(rest, ")") {
		return "", false
	}
	if opt := strings.TrimSpace(rest[1:]); opt != "" {
		return opt, true
	}
	return "", false
}

func questionBody(question string, options []string) string {
	if len(options) == 0 {
		return neutralize(question)
	}
	numbered := make([]string, len(options))
	for i, opt := range options {
		numbered[i] = strconv.Itoa(i+1) + ". " + opt
	}
	return neutralize(question + "\n\n" + strings.Join(numbered, "\n"))
}

func parkFooter(reply, others string) string {
	return "\n\n---\n\nReply " + reply + "\nOther commands: " + others
}

func questionFooter() string {
	return parkFooter("`/answer <your choice>`. ghafk continues on the next tick.", "`/retry` · `/stop`")
}

func retryFooter() string {
	return parkFooter("`/retry`. ghafk continues on the next tick.", "`/close` · `/stop`")
}

func parkOnError(at parkPlace, role, sentence string, err error) error {
	spec := commentSpec{kind: "park", role: role, number: at.issue, sentence: sentence, body: fenced(err.Error()), footer: retryFooter()}
	return park(at, sentence, spec)
}

func approvalFooter() string {
	return parkFooter("`/start` to approve the current text. ghafk continues on the next tick.", "`/close` · `/stop`")
}

func closeFooter() string {
	return parkFooter("`/close`. ghafk closes the issue as completed.", "`/retry` · `/stop`")
}

func engineComment(body string) bool {
	return strings.HasPrefix(body, "<!-- ghafk:")
}

type parkPlace struct {
	repo, base, login, label string
	target                   string
	issue                    int
	prior                    []prComment
	card                     *card
}

func park(at parkPlace, reason string, spec commentSpec) error {
	stepf(at.base, at.issue, "needs-human")
	if at.card != nil {
		at.card.fail(reason)
	}
	if err := postComment(at.repo, at.target, spec, at.prior, at.login); err != nil {
		return err
	}
	return needHuman(at.repo, strconv.Itoa(at.issue), at.label)
}
