package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cardPrefix        = "<!-- ghafk:card -->"
	statePrefix       = "<!-- ghafk:state "
	workingLabel      = "ghafk:working"
	cardLimit         = 60000
	assumptionsNotice = "Assumptions are too long for this card."
)

var cardSteps = []string{"Groom", "Implement", "Checks", "Judge", "Merge"}

type cardStep struct {
	Status  string `json:"status,omitempty"`
	Started string `json:"started,omitempty"`
	Ended   string `json:"ended,omitempty"`
	Tokens  int    `json:"tokens,omitempty"`
	Harness string `json:"harness,omitempty"`
}

type cardItem struct {
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
	Owner  bool   `json:"owner,omitempty"`
}

type cardState struct {
	Number   int                 `json:"number"`
	Title    string              `json:"title"`
	Phase    string              `json:"phase"`
	Step     string              `json:"step,omitempty"`
	Steps    map[string]cardStep `json:"steps,omitempty"`
	PR       string              `json:"pr,omitempty"`
	Reason   string              `json:"reason,omitempty"`
	Repairs  int                 `json:"repairs,omitempty"`
	Contract string              `json:"contract,omitempty"`
	Approved string              `json:"approved,omitempty"`
	Items    map[int]cardItem    `json:"items,omitempty"`
	History  []cardEvent         `json:"history,omitempty"`
}

type cardEvent struct {
	Time   string `json:"time"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

func escapeComment(s string) string {
	return strings.ReplaceAll(s, "--", "-\\u002d")
}

func unescapeComment(s string) string {
	return strings.ReplaceAll(s, "-\\u002d", "--")
}

func encodeState(st cardState) string {
	data, _ := json.Marshal(st)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func decodeState(payload string) (cardState, bool) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return cardState{}, false
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return cardState{}, false
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return cardState{}, false
	}
	var st cardState
	if err := json.Unmarshal(data, &st); err != nil {
		return cardState{}, false
	}
	return st, true
}

func stepDuration(s cardStep) string {
	start, err1 := time.Parse(time.RFC3339, s.Started)
	end, err2 := time.Parse(time.RFC3339, s.Ended)
	if err1 != nil || err2 != nil || end.Before(start) {
		return ""
	}
	d := end.Sub(start).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func localTime(started string, loc *time.Location) string {
	if started == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return started
	}
	return t.In(loc).Format("Jan 2 15:04 MST")
}

func shortTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return strconv.Itoa((n+500)/1000) + "k"
	}
	return strconv.Itoa(n)
}

func renderCard(st cardState, loc *time.Location) string {
	body := renderCardBody(st, loc, false)
	if len(body) > cardLimit {
		body = renderCardBody(st, loc, true)
	}
	return body
}

func renderCardBody(st cardState, loc *time.Location, trim bool) string {
	var b strings.Builder
	b.WriteString(cardPrefix + "\n")
	b.WriteString(statePrefix + encodeState(st) + " -->\n\n")
	heading := "## ghafk status for #"
	if st.Contract != "" {
		heading = "## Contract for #"
	}
	b.WriteString(heading + strconv.Itoa(st.Number) + ": " + neutralize(st.Title) + "\n\n")
	b.WriteString(renderAlert(st, loc) + "\n\n")
	if st.Contract != "" {
		b.WriteString(renderContract(neutralize(st.Contract), st.Items, trim) + "\n\n")
	}
	b.WriteString("### Progress\n\n" + progressHeader(progressColumns))
	for _, name := range cardSteps {
		b.WriteString(renderStep(name, st.Repairs, st.Steps[name], loc, progressColumns) + "\n")
	}
	if history := renderHistory(st.History, loc); history != "" {
		b.WriteString("\n" + history + "\n")
	}
	return b.String()
}

func renderContract(contract string, items map[int]cardItem, trim bool) string {
	section, inDetails := "", false
	k := 0
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(contract), "\n") {
		name, ok := strings.CutPrefix(line, "## ")
		if !ok {
			if section == "Acceptance" {
				if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "- ") {
					k++
					line = renderContractItem(trimmed[2:], items[k])
				}
			}
			if section == "Assumptions" && trim {
				continue
			}
			out = append(out, line)
			continue
		}
		name = strings.TrimSpace(name)
		if name == "Assumptions" && trim {
			out = append(out, assumptionsNotice)
			section = name
			continue
		}
		if name == "Files" || name == "Assumptions" {
			if !inDetails {
				out = append(out, "<details><summary>Files and assumptions</summary>", "")
				inDetails = true
			}
			out = append(out, "### "+name)
		} else {
			if inDetails {
				out = append(out, "", "</details>", "")
				inDetails = false
			}
			if name == "Problem" || name == "Change" || name == "Commit" || name == "Acceptance" {
				line = "### " + name
			}
			out = append(out, line)
		}
		section = name
	}
	if inDetails {
		out = append(out, "", "</details>")
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n")
}

func renderContractItem(item string, it cardItem) string {
	item = strings.TrimSpace(item)
	if rest, ok := strings.CutPrefix(item, "manual:"); ok {
		item = "**manual:** " + strings.TrimSpace(rest)
	}
	box := " "
	switch {
	case it.Status == "pass":
		box = "x"
	case it.Status == "manual" && it.Owner:
		box = "x"
	}
	line := "- [" + box + "] " + item
	if it.Status == "fail" && it.Reason != "" {
		line += " ⟵ *judge: " + neutralize(clipReason(it.Reason)) + "*"
	}
	return line
}

func clipReason(reason string) string {
	r := []rune(reason)
	if len(r) > 120 {
		r = r[:120]
	}
	return string(r)
}

func manualItems(contract string) map[int]string {
	items := map[int]string{}
	k := 0
	for _, line := range sectionLines(contract, "## ", "Acceptance") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		k++
		if rest, ok := strings.CutPrefix(strings.TrimSpace(trimmed[2:]), "manual:"); ok {
			items[k] = strings.TrimSpace(rest)
		}
	}
	return items
}

func manualChecks(st cardState) []string {
	manual := manualItems(st.Contract)
	keys := make([]int, 0, len(manual))
	for k := range manual {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var out []string
	for _, k := range keys {
		if it := st.Items[k]; it.Status == "pass" || it.Owner {
			continue
		}
		out = append(out, manual[k])
	}
	return out
}

func renderAlert(st cardState, loc *time.Location) string {
	lines := []string{"> [!NOTE]"}
	switch st.Phase {
	case "parked":
		lines = []string{"> [!WARNING]", "> Parked in needs-human: " + neutralize(scrubEmDashes(st.Reason))}
	case "merged":
		lines = []string{"> [!TIP]", "> Merged."}
		if checks := manualChecks(st); len(checks) > 0 {
			lines = append(lines, "> Check by hand:")
			for _, text := range checks {
				lines = append(lines, "> - "+text)
			}
		}
	case "working":
		lines = append(lines, "> Working on "+st.Step+" since "+localTime(st.Steps[st.Step].Started, loc)+".")
	default:
		lines = append(lines, "> Queued; last step "+lastStep(st)+".")
	}
	if st.PR != "" {
		lines = append(lines, "> PR: "+st.PR)
	}
	if total := tokenTotal(st); total > 0 {
		lines = append(lines, "> Tokens so far: "+shortTokens(total)+".")
	}
	return strings.Join(lines, "\n")
}

func lastStep(st cardState) string {
	name := "none yet"
	for _, s := range cardSteps {
		if _, ok := st.Steps[s]; ok {
			name = s
		}
	}
	return name
}

func tokenTotal(st cardState) int {
	total := 0
	for _, s := range st.Steps {
		total += s.Tokens
	}
	return total
}

func progressHeader(cols []string) string {
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = strings.ToUpper(c[:1]) + c[1:]
	}
	return "| " + strings.Join(titles, " | ") + " |\n|" + strings.Repeat("---|", len(cols)) + "\n"
}

func renderStep(name string, repairs int, s cardStep, loc *time.Location, cols []string) string {
	label := name
	if repairs > 0 && (name == "Implement" || name == "Checks" || name == "Judge") {
		label = name + " (repair " + strconv.Itoa(repairs) + ")"
	}
	icon := "⬜"
	switch s.Status {
	case "done":
		icon = "✅"
	case "running":
		icon = "⏳"
	case "failed":
		icon = "❌"
	}
	tokens := ""
	if s.Tokens > 0 {
		tokens = shortTokens(s.Tokens)
	}
	cells := map[string]string{"step": label, "status": icon, "started": localTime(s.Started, loc), "ended": localTime(s.Ended, loc), "duration": stepDuration(s), "tokens": tokens, "harness": s.Harness}
	row := make([]string, len(cols))
	for i, c := range cols {
		row[i] = cells[c]
	}
	return "| " + strings.Join(row, " | ") + " |"
}

func renderHistory(events []cardEvent, loc *time.Location) string {
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<details><summary>History</summary>\n\n")
	for _, e := range events {
		b.WriteString(localTime(e.Time, loc) + ": " + eventText(e) + "\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n\n</details>"
}

func eventText(e cardEvent) string {
	if e.Reason != "" {
		return e.Name + ": " + neutralize(e.Reason)
	}
	return e.Name
}

func parseCard(body string) (cardState, bool) {
	if !strings.HasPrefix(body, cardPrefix) {
		return cardState{}, false
	}
	rest := strings.TrimLeft(strings.TrimPrefix(body, cardPrefix), "\r\n \t")
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, statePrefix) || !strings.HasSuffix(rest, " -->") {
		return cardState{}, false
	}
	payload := rest[len(statePrefix) : len(rest)-len(" -->")]
	if st, ok := decodeState(payload); ok {
		return st, true
	}
	var st cardState
	if err := json.Unmarshal([]byte(unescapeComment(payload)), &st); err != nil {
		return cardState{}, false
	}
	return st, true
}

const maxHistory = 30

type card struct {
	repo string
	n    string
	id   string
	url  string
	st   cardState
	err  error
}

func openCard(repo string, is issue, login string) *card {
	c := &card{repo: repo, n: strconv.Itoa(is.Number), st: cardState{Number: is.Number, Title: is.Title, Phase: "queued"}}
	for _, cm := range is.Comments {
		if cm.Author.Login != login || !strings.HasPrefix(cm.Body, cardPrefix) {
			continue
		}
		c.id = commentID(cm.URL)
		if st, ok := parseCard(cm.Body); ok {
			c.st = st
		}
	}
	return c
}

func (c *card) put() {
	if c.id != "" {
		if body, err := gh(c.repo, "api", "repos/{owner}/{repo}/issues/comments/"+c.id, "--jq", ".body"); err != nil {
			fmt.Fprintf(os.Stderr, "ghafk: card: %v\n", err)
		} else {
			c.applyOwnerTicks(body)
		}
	}
	body := renderCard(c.st, time.Local)
	if c.id != "" {
		if _, err := gh(c.repo, "api", "-X", "PATCH", "repos/{owner}/{repo}/issues/comments/"+c.id, "-f", "body="+body); err != nil {
			c.err = err
			fmt.Fprintf(os.Stderr, "ghafk: card: %v\n", err)
		}
		return
	}
	out, err := gh(c.repo, "issue", "comment", c.n, "--body", body)
	if err != nil {
		c.err = err
		fmt.Fprintf(os.Stderr, "ghafk: card: %v\n", err)
		return
	}
	c.url = out
	c.id = commentID(out)
}

func (c *card) supersede(harness string) {
	old := c.id
	c.id = ""
	c.url = ""
	c.st = cardState{Number: c.st.Number, Title: c.st.Title}
	c.begin("Groom", harness)
	if old == "" || c.url == "" {
		return
	}
	body := "<details><summary>Superseded</summary>\n\nSuperseded by " + c.url + "\n\n</details>"
	if _, err := gh(c.repo, "api", "-X", "PATCH", "repos/{owner}/{repo}/issues/comments/"+old, "-f", "body="+body); err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: card: %v\n", err)
	}
}

func (c *card) setPR(url string) {
	c.st.PR = url
}

func (c *card) setItems(items map[int]cardItem) {
	if len(items) == 0 {
		return
	}
	if c.st.Items == nil {
		c.st.Items = map[int]cardItem{}
	}
	for k, it := range items {
		c.st.Items[k] = it
	}
}

func (c *card) applyOwnerTicks(body string) {
	if c.st.Items == nil {
		c.st.Items = map[int]cardItem{}
	}
	for k, ticked := range ownerTicks(body) {
		it := c.st.Items[k]
		it.Owner = ticked
		if it == (cardItem{}) {
			delete(c.st.Items, k)
			continue
		}
		c.st.Items[k] = it
	}
}

func ownerTicks(body string) map[int]bool {
	ticks := map[int]bool{}
	k := 0
	for _, line := range sectionLines(body, "### ", "Acceptance") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "- [")
		if !ok || len(rest) < 2 || rest[1] != ']' || (rest[0] != ' ' && rest[0] != 'x' && rest[0] != 'X') {
			continue
		}
		k++
		if item := strings.TrimSpace(rest[2:]); strings.HasPrefix(item, "**manual:**") {
			ticks[k] = rest[0] != ' '
		}
	}
	return ticks
}

func (c *card) record(event, reason string) {
	c.st.History = append(c.st.History, cardEvent{Time: time.Now().UTC().Format(time.RFC3339), Name: event, Reason: reason})
	if len(c.st.History) > maxHistory {
		c.st.History = c.st.History[len(c.st.History)-maxHistory:]
	}
}

func (c *card) note(event string) {
	if c.id == "" {
		return
	}
	c.record(event, "")
	c.put()
}

func (c *card) begin(step, harness string) {
	c.st.Phase = "working"
	c.st.Reason = ""
	c.st.Step = step
	if c.st.Steps == nil {
		c.st.Steps = map[string]cardStep{}
	}
	c.st.Steps[step] = cardStep{Status: "running", Started: time.Now().UTC().Format(time.RFC3339), Harness: harness}
	c.put()
}

func (c *card) mark(status string, tokens int) {
	if c.st.Step == "" {
		return
	}
	if c.st.Steps == nil {
		c.st.Steps = map[string]cardStep{}
	}
	s := c.st.Steps[c.st.Step]
	s.Status = status
	if status != "running" {
		s.Ended = time.Now().UTC().Format(time.RFC3339)
	}
	if tokens > 0 {
		s.Tokens = tokens
	}
	c.st.Steps[c.st.Step] = s
}

func (c *card) finish(status string, tokens int) {
	c.mark(status, tokens)
	c.put()
}

func (c *card) queue() {
	c.st.Phase = "queued"
	c.st.Reason = ""
	c.st.Step = ""
	c.put()
}

func (c *card) fail(reason string) {
	c.mark("failed", 0)
	c.st.Phase = "parked"
	c.st.Reason = reason
	c.st.Step = ""
	c.record("parked", reason)
	c.put()
}

func (c *card) merged() {
	c.mark("done", 0)
	c.st.Phase = "merged"
	c.st.Reason = ""
	c.st.Step = ""
	c.record("merged", "")
	c.put()
}

func (c *card) repair(harness string) {
	c.st.Repairs++
	delete(c.st.Steps, "Checks")
	delete(c.st.Steps, "Judge")
	c.record("repair started", "")
	c.begin("Implement", harness)
}

func (c *card) approve(digest string) {
	c.st.Approved = digest
	c.record("approved", "")
	c.put()
}
