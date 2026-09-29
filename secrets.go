package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	secretPlaceholder = "[ghafk removed a value that looks like a secret]"
	secretMinLength   = 12
	secretRepairs     = 2
)

var secretRegistry struct {
	sync.Mutex
	forms map[string]bool
}

const knownSecretRule = "a value ghafk passed into the run"

func registerSecret(value string) {
	value = strings.TrimSpace(value)
	if len(value) < secretMinLength {
		return
	}
	secretRegistry.Lock()
	defer secretRegistry.Unlock()
	if secretRegistry.forms == nil {
		secretRegistry.forms = map[string]bool{}
	}
	for _, form := range secretForms(value) {
		if len(form) >= secretMinLength {
			secretRegistry.forms[form] = true
		}
	}
}

func registerOwnerToken(dir string) {
	if token, err := run(dir, "gh", "auth", "token"); err == nil {
		registerSecret(token)
	}
}

func registerSecretFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if v, err := decodeLoginJSON(data); err == nil {
		registerJSONStrings(v)
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "-----") {
			registerSecret(line)
		}
	}
}

func registerJSONStrings(v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, item := range t {
			registerJSONStrings(item)
		}
	case []any:
		for _, item := range t {
			registerJSONStrings(item)
		}
	case string:
		if len(t) >= 20 && !strings.ContainsAny(t, " \t\n") {
			registerSecret(t)
		}
	}
}

var credentialName = regexp.MustCompile(`(?i)(key|token|secret|passw|credential|auth)`)

var credentialLine = regexp.MustCompile(`(?i)^\s*["']?[A-Za-z0-9_.-]*(?:key|token|secret|passw|credential|auth)[A-Za-z0-9_.-]*["']?\s*[=:]\s*["']?([^"'\s#,]+)`)

var configFileExt = map[string]bool{".json": true, ".jsonc": true, ".toml": true, ".yaml": true, ".yml": true}

var credentialReference = regexp.MustCompile(`^(?:[A-Z_][A-Z0-9_]*|\$\{?[A-Za-z_][A-Za-z0-9_]*\}?|\{env:[^}]*\}|process\.env\.[A-Za-z0-9_.]+)$`)

func registerConfigValue(value string) {
	if !credentialReference.MatchString(strings.TrimSpace(value)) {
		registerSecret(value)
	}
}

func registerConfigSecrets(path string) {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if err != nil || d.Type()&os.ModeSymlink != 0 || d.IsDir() || !configFileExt[filepath.Ext(p)] {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > loginSizeLimit {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if v, err := decodeLoginJSON(data); err == nil {
			registerCredentialFields(v, "")
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if m := credentialLine.FindStringSubmatch(line); m != nil {
				registerConfigValue(m[1])
			}
		}
		return nil
	})
}

func registerTokenFields(v any, key string) {
	switch t := v.(type) {
	case map[string]any:
		for k, item := range t {
			registerTokenFields(item, k)
		}
	case string:
		if loginTokenFields[key] {
			registerSecret(t)
		}
	}
}

func registerCredentialFields(v any, key string) {
	switch t := v.(type) {
	case map[string]any:
		for k, item := range t {
			registerCredentialFields(item, k)
		}
	case []any:
		for _, item := range t {
			registerCredentialFields(item, key)
		}
	case string:
		if credentialName.MatchString(key) {
			registerConfigValue(t)
		}
	}
}

func secretForms(value string) []string {
	forms := []string{value}
	raw := []byte(value)
	for shift := 0; shift < 3; shift++ {
		prefixed := append(make([]byte, shift), raw...)
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			encoded := enc.EncodeToString(prefixed)
			start := (shift*8 + 5) / 6
			end := len(encoded)
			if tail := (len(prefixed) % 3); tail != 0 {
				end--
			}
			if start < end {
				forms = append(forms, encoded[start:end])
			}
		}
	}
	lower := hex.EncodeToString(raw)
	forms = append(forms, lower, strings.ToUpper(lower))
	if escaped := url.QueryEscape(value); escaped != value {
		forms = append(forms, escaped)
	}
	return dedup(forms)
}

var secretPatterns = []struct {
	rule string
	re   string
}{
	{"an Anthropic key", `sk-ant-[A-Za-z0-9_-]{20,}`},
	{"an OpenAI key", `sk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{20,}`},
	{"an OpenAI key", `sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}`},
	{"a GitHub token", `gh[pousr]_[A-Za-z0-9]{36,}`},
	{"a GitHub token", `github_pat_[A-Za-z0-9_]{22,}`},
	{"an AWS access key", `A(?:KIA|SIA)[0-9A-Z]{16}`},
	{"a private key", `-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----`},
	{"a Slack token", `xox[baprs]-[0-9A-Za-z-]{10,}`},
	{"a Google API key", `AIza[0-9A-Za-z_-]{35}`},
}

var secretPattern = func() *regexp.Regexp {
	var alts []string
	for _, p := range secretPatterns {
		alts = append(alts, "("+p.re+")")
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9+/_-])(?:` + strings.Join(alts, "|") + `)`)
}()

type secretMatch struct {
	rule  string
	value string
	known bool
}

func findSecrets(text string) []secretMatch {
	var found []secretMatch
	secretRegistry.Lock()
	for form := range secretRegistry.forms {
		if strings.Contains(text, form) {
			found = append(found, secretMatch{rule: knownSecretRule, value: form, known: true})
		}
	}
	secretRegistry.Unlock()
	for _, m := range secretPattern.FindAllStringSubmatch(text, -1) {
		for i, group := range m[1:] {
			if group != "" {
				found = append(found, secretMatch{rule: secretPatterns[i].rule, value: group})
				break
			}
		}
	}
	return found
}

func redactSecrets(text string) string {
	matches := findSecrets(text)
	sort.Slice(matches, func(i, j int) bool { return len(matches[i].value) > len(matches[j].value) })
	for _, m := range matches {
		text = strings.ReplaceAll(text, m.value, secretPlaceholder)
	}
	return text
}

var contentFlags = map[string]bool{"--body": true, "--title": true, "--subject": true}

func redactArgs(args []string) []string {
	out := append([]string(nil), args...)
	removed := 0
	for i, arg := range out {
		switch {
		case i > 0 && contentFlags[out[i-1]]:
			out[i] = redactSecrets(arg)
		case strings.HasPrefix(arg, "body="):
			out[i] = "body=" + redactSecrets(strings.TrimPrefix(arg, "body="))
		}
		removed += strings.Count(out[i], secretPlaceholder) - strings.Count(arg, secretPlaceholder)
	}
	if removed > 0 {
		fmt.Fprintf(os.Stderr, "ghafk: gh %s: removed %d value(s) that look like secrets\n", out[0], removed)
	}
	return out
}

type secretHit struct {
	where string
	rule  string
}

func (h secretHit) String() string { return h.where + ": " + h.rule }

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

var plainDiff = []string{"--no-color", "--no-ext-diff", "--no-textconv", "--no-relative"}

type addedLine struct {
	number int
	text   string
}

func addedLines(patch string) []addedLine {
	var added []addedLine
	oldLeft, newLeft, line := 0, 0, 0
	for _, text := range strings.Split(patch, "\n") {
		if oldLeft == 0 && newLeft == 0 {
			if m := hunkHeader.FindStringSubmatch(text); m != nil {
				oldLeft, newLeft = hunkCount(m[1]), hunkCount(m[3])
				line, _ = strconv.Atoi(m[2])
			}
			continue
		}
		switch {
		case strings.HasPrefix(text, "\\"):
		case strings.HasPrefix(text, "-"):
			oldLeft--
		case strings.HasPrefix(text, "+"):
			added = append(added, addedLine{line, text[1:]})
			line++
			newLeft--
		}
	}
	return added
}

func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func scanChange(work, base string, allow []string) ([]secretHit, error) {
	names, err := runExact(work, "git", nil, append([]string{"diff", "--name-only", "-z", "--no-renames"}, append(plainDiff, base, "HEAD")...)...)
	if err != nil {
		return nil, err
	}
	messages, err := run(work, "git", "log", "--no-color", "--no-show-signature", "--format=%B", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var candidates []struct {
		hit   secretHit
		match secretMatch
	}
	add := func(where string, path string, text string) {
		for _, m := range findSecrets(text) {
			if !m.known && path != "" && secretAllowed(path, allow) {
				continue
			}
			candidates = append(candidates, struct {
				hit   secretHit
				match secretMatch
			}{secretHit{where, m.rule}, m})
		}
	}
	for _, path := range strings.Split(names, "\x00") {
		if path == "" {
			continue
		}
		add("a file name", "", path)
		patch, err := run(work, "git", append(append([]string{"diff", "--text", "--unified=0", "--no-renames"}, plainDiff...), base, "HEAD", "--", ":(literal)"+path)...)
		if err != nil {
			return nil, err
		}
		for _, l := range addedLines(patch) {
			add(fmt.Sprintf("%s line %d", strconv.Quote(path), l.number), path, l.text)
		}
	}
	add("the commit message", "", messages)
	if len(candidates) == 0 {
		return nil, nil
	}
	old, err := secretsInTree(work, base)
	if err != nil {
		return nil, err
	}
	var hits []secretHit
	seen := map[string]bool{}
	for _, c := range candidates {
		if !c.match.known && old[c.match.value] {
			continue
		}
		if key := c.hit.String(); !seen[key] {
			seen[key] = true
			hits = append(hits, c.hit)
		}
	}
	return hits, nil
}

func secretsInTree(work, base string) (map[string]bool, error) {
	var alts []string
	for _, p := range secretPatterns {
		alts = append(alts, strings.ReplaceAll(p.re, "(?:", "("))
	}
	out, err := run(work, "git", "grep", "--no-color", "--no-line-number", "--no-column", "-I", "-h", "-o", "-E", "-e", strings.Join(alts, "|"), base)
	old := map[string]bool{}
	if err != nil {
		if grepNoMatch.MatchString(err.Error()) {
			return old, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		old[strings.TrimSpace(line)] = true
	}
	return old, nil
}

var grepNoMatch = regexp.MustCompile(`exit status 1(?::|$)`)

func secretAllowed(path string, allow []string) bool {
	for _, prefix := range allow {
		if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func squashOnto(work, base string, messages ...string) (bool, error) {
	if _, err := run(work, "git", "add", "-A"); err != nil {
		return false, err
	}
	if _, err := run(work, "git", "reset", "--soft", base); err != nil {
		return false, err
	}
	if _, err := run(work, "git", "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	args := []string{"commit"}
	for _, m := range messages {
		args = append(args, "-m", m)
	}
	_, err := run(work, "git", args...)
	return err == nil, err
}

func hitLines(hits []secretHit) string {
	var lines []string
	for _, h := range hits {
		lines = append(lines, "- "+h.String())
	}
	return strings.Join(lines, "\n")
}

func secretFeedback(hits []secretHit) string {
	return "# ghafk did not push your change\n\nThese places hold a value that looks like a secret:\n\n" + hitLines(hits) +
		"\n\nRemove each value. Do not write keys, tokens or passwords into files, file names or commit messages. Read them from environment variables at run time instead."
}

func secretParkBody(hits []secretHit, asked bool) string {
	body := fenced(hitLines(hits)) + "\n\n"
	if asked {
		body += "ghafk asked the worker to remove the values, and it did not. "
	}
	return body + "Rotate a real secret. If a match is a test value, add its path to a `secrets-allow:` line in `.ghafk/WORKFLOW.md`."
}

var scannedPush = map[string]string{}

func markScanned(work string) {
	if head, err := run(work, "git", "rev-parse", "HEAD"); err == nil {
		scannedPush[work] = head
	}
}

func pushAllowed(dir string) error {
	if _, tracked := worktreeGitDirs[dir]; !tracked {
		return nil
	}
	head, err := run(dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if scannedPush[dir] != head {
		return fmt.Errorf("ghafk refuses to push %s: the change was not checked for secrets", filepath.Base(dir))
	}
	return nil
}

func parkSecrets(at parkPlace, role string, n int, hits []secretHit) error {
	spec := commentSpec{kind: "park", role: role, number: n, sentence: "The change holds a value that looks like a secret. ghafk did not push it.", body: secretParkBody(hits, true), footer: retryFooter()}
	return park(at, "secret in the change", spec)
}
