package main

import "strings"

func contractText(comments []prComment, login string) string {
	text := ""
	for _, c := range comments {
		if contract, ok := contractIn(c, login); ok {
			text = contract
		}
	}
	return text
}

func contractIn(c prComment, login string) (string, bool) {
	if c.Author.Login != login {
		return "", false
	}
	if strings.HasPrefix(c.Body, cardPrefix) {
		if st, ok := parseCard(c.Body); ok && st.Contract != "" {
			return st.Contract, true
		}
	}
	return "", false
}

func answerFrom(output string, markers ...string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.Trim(line, "`*# \t")
		for _, marker := range markers {
			if !strings.HasPrefix(trimmed, marker) {
				continue
			}
			rest := strings.TrimLeft(trimmed[len(marker):], "`*# \t")
			if rest == "" {
				lines[i] = marker
			} else {
				lines[i] = marker + " " + rest
			}
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(output)
}

var commitTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"refactor": true,
	"docs":     true,
	"test":     true,
	"chore":    true,
}

func validCommitSubject(line string) bool {
	if line == "" || len([]rune(line)) > 72 || strings.ContainsAny(line, "@") || strings.Contains(line, "://") {
		return false
	}
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '#' && line[i+1] >= '0' && line[i+1] <= '9' {
			return false
		}
	}
	head, desc, ok := strings.Cut(line, ": ")
	if !ok {
		return false
	}
	typ, scope := head, ""
	if open := strings.IndexByte(head, '('); open >= 0 {
		if !strings.HasSuffix(head, ")") || len(head)-open < 3 {
			return false
		}
		typ, scope = head[:open], head[open+1:len(head)-1]
	}
	if !commitTypes[typ] {
		return false
	}
	for _, r := range scope {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	d := []rune(desc)
	if len(d) == 0 || d[0] < 'a' || d[0] > 'z' {
		return false
	}
	return d[len(d)-1] != '.'
}

func commitSubject(contract, title string) string {
	if line, ok := contractCommitLine(contract); ok {
		return line
	}
	return fallbackCommitSubject(title)
}

func contractCommitLine(contract string) (string, bool) {
	for _, line := range sectionLines(contract, "## ", "Commit") {
		trimmed := strings.Trim(strings.TrimSpace(line), "`")
		if trimmed == "" {
			continue
		}
		return trimmed, validCommitSubject(trimmed)
	}
	return "", false
}

func fallbackCommitSubject(title string) string {
	var words []string
	for _, w := range strings.Fields(removeIssueNumbers(title)) {
		if w = strings.ReplaceAll(w, "@", ""); w != "" && !strings.Contains(w, "://") {
			words = append(words, w)
		}
	}
	s := strings.Join(words, " ")
	s = strings.TrimRight(s, ".")
	r := []rune(s)
	for i := 0; i < len(r) && r[i] >= 'A' && r[i] <= 'Z'; i++ {
		if i > 0 && i+1 < len(r) && r[i+1] >= 'a' && r[i+1] <= 'z' {
			break
		}
		r[i] += 'a' - 'A'
	}
	limit := 72 - len("feat: ")
	if len(r) > limit {
		cut := string(r[:limit])
		if sp := strings.LastIndex(cut, " "); sp > 0 {
			cut = cut[:sp]
		}
		r = []rune(strings.TrimRight(cut, " ."))
	}
	return "feat: " + string(r)
}

func removeIssueNumbers(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '#' {
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j > i+1 {
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func staleAfterContract(comments []prComment, login string) bool {
	last := ""
	for _, c := range comments {
		if _, ok := contractIn(c, login); ok {
			last = "contract"
		} else if c.Author.Login == login && strings.HasPrefix(c.Body, reconcileStaleMarker) {
			last = "stale"
		}
	}
	return last == "stale"
}

func contractFiles(contract string) []string {
	var files []string
	for _, line := range sectionLines(contract, "## ", "Files") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
		if line == "" {
			continue
		}
		if _, rest, ok := strings.Cut(line, "`"); ok {
			if path, _, _ := strings.Cut(rest, "`"); path != "" {
				files = append(files, path)
				continue
			}
		}
		files = append(files, strings.Fields(line)[0])
	}
	return files
}

func overlaps(files, landed []string) bool {
	for _, f := range files {
		for _, l := range landed {
			if l == f || (strings.HasSuffix(f, "/") && strings.HasPrefix(l, f)) {
				return true
			}
		}
	}
	return false
}

func headingLevel(line string) int {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || !strings.HasPrefix(line[n:], " ") {
		return 0
	}
	return n
}

func sectionLines(doc, prefix, name string) []string {
	level := headingLevel(prefix)
	var lines []string
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if l := headingLevel(line); l > 0 && l <= level {
			if in {
				break
			}
			in = l == level && strings.TrimSpace(line[l:]) == name
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	return lines
}
