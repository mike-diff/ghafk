package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRenderCardAlertAndTableMatchThePhase(t *testing.T) {
	useColumns(t, "step", "status", "started", "tokens", "harness")
	steps := map[string]cardStep{
		"Groom":     {Status: "done", Started: "2026-09-24T18:00:00Z", Tokens: 230672},
		"Implement": {Status: "running", Started: "2026-09-24T18:05:00Z"},
	}
	cases := []struct{ phase, marker string }{
		{"queued", "[!NOTE]"},
		{"working", "[!NOTE]"},
		{"parked", "[!WARNING]"},
		{"merged", "[!TIP]"},
	}
	for _, tc := range cases {
		st := cardState{Number: 9, Title: "rename the force subcommand", Phase: tc.phase, Step: "Implement", Steps: steps, PR: "https://github.com/mike-diff/ghafk/pull/12", Reason: "which name?"}
		body := renderCard(st, time.UTC)
		if !strings.HasPrefix(body, cardPrefix+"\n") {
			t.Fatalf("phase %s: card does not begin with the marker line in\n%s", tc.phase, body)
		}
		if !strings.Contains(body, "> "+tc.marker) {
			t.Fatalf("phase %s: alert is not %s in\n%s", tc.phase, tc.marker, body)
		}
		if !strings.Contains(body, "## ghafk status for #9: rename the force subcommand") {
			t.Fatalf("phase %s: heading missing in\n%s", tc.phase, body)
		}
		if !strings.Contains(body, "| Groom | ✅ | Sep 24 18:00 UTC | 231k |") {
			t.Fatalf("phase %s: done row wrong in\n%s", tc.phase, body)
		}
		if !strings.Contains(body, "| Implement | ⏳ | Sep 24 18:05 UTC |  |") {
			t.Fatalf("phase %s: running row wrong in\n%s", tc.phase, body)
		}
	}
	working := renderCard(cardState{Number: 9, Title: "t", Phase: "working", Step: "Implement", Steps: steps}, time.UTC)
	if !strings.Contains(working, "> Working on Implement since Sep 24 18:05 UTC.") {
		t.Fatalf("working alert does not name the step and start:\n%s", working)
	}
	if !strings.Contains(working, "> Tokens so far: 231k.") {
		t.Fatalf("working alert does not total tokens:\n%s", working)
	}
	queued := renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Steps: steps}, time.UTC)
	if !strings.Contains(queued, "> Queued; last step Implement.") {
		t.Fatalf("queued alert does not name the last step:\n%s", queued)
	}
	parked := renderCard(cardState{Number: 9, Title: "t", Phase: "parked", Reason: "which name?"}, time.UTC)
	if !strings.Contains(parked, "> [!WARNING]\n> Parked in needs-human: which name?") {
		t.Fatalf("parked alert does not carry the reason:\n%s", parked)
	}
	repaired := renderCard(cardState{Number: 9, Title: "t", Phase: "working", Step: "Implement", Repairs: 1, Steps: map[string]cardStep{"Implement": {Status: "running", Started: "2026-09-24T18:05:00Z"}}}, time.UTC)
	if !strings.Contains(repaired, "| Implement (repair 1) | ⏳ |") || !strings.Contains(repaired, "| Checks (repair 1) | ⬜ |") {
		t.Fatalf("repair attempt not shown on the re-run rows:\n%s", repaired)
	}
}

func TestRenderCardShowsTheContractAsMarkdown(t *testing.T) {
	contract := "## Problem\n\nThe raw `contract:` marker is unreadable.\n\n## Change\n\n`cardState` gains a `Contract` field.\n\n## Acceptance\n\n- `go test -count=1 ./...` → ok\n- manual: read the card\n- a plain line stays\n\n## Files\n\n- `card.go`\n\n## Assumptions\n\n- flags like `--count` stay escaped.\n"
	body := renderCard(cardState{Number: 10, Title: "render the contract", Phase: "queued", Contract: contract}, time.UTC)
	for _, want := range []string{
		"## Contract for #10: render the contract",
		"### Problem\n\nThe raw `contract:` marker is unreadable.",
		"### Change",
		"- [ ] `go test -count=1 ./...` → ok",
		"- [ ] **manual:** read the card",
		"- [ ] a plain line stays",
		"<details><summary>Files and assumptions</summary>\n\n### Files\n\n- `card.go`\n\n### Assumptions\n\n- flags like `--count` stay escaped.\n\n</details>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("contract render missing %q in\n%s", want, body)
		}
	}
	if heading, alert, problem := strings.Index(body, "## Contract for #10"), strings.Index(body, "> [!NOTE]"), strings.Index(body, "### Problem"); heading < 0 || !(heading < alert && alert < problem) {
		t.Fatalf("contract must sit between the alert and Progress in\n%s", body)
	}
	plain := renderCard(cardState{Number: 10, Title: "render the contract", Phase: "queued"}, time.UTC)
	if strings.Contains(plain, "Contract") {
		t.Fatalf("a card without a contract must render as before:\n%s", plain)
	}
}

func TestCardStateRoundTripsThroughRenderAndParse(t *testing.T) {
	st := cardState{
		Number:  9,
		Title:   "rename --force to --yes",
		Phase:   "working",
		Step:    "Implement",
		Steps:   map[string]cardStep{"Groom": {Status: "done", Started: "2026-09-24T18:00:00Z", Tokens: 12}},
		PR:      "https://github.com/mike-diff/ghafk/pull/12",
		Reason:  "a--b",
		Repairs: 1,
		Items:   map[int]cardItem{2: {Status: "fail", Reason: "exit 1: a--b", Owner: true}},
	}
	body := renderCard(st, time.UTC)
	stateLine := strings.Split(body, "\n")[1]
	if strings.Contains(stateLine, "--force") {
		t.Fatalf("a bare -- in the state line closes the comment:\n%s", stateLine)
	}
	got, ok := parseCard(body)
	if !ok {
		t.Fatalf("parseCard rejected the rendered card:\n%s", body)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("round trip got %#v, want %#v", got, st)
	}
	if _, ok := parseCard("contract:\n\n## Problem"); ok {
		t.Fatal("parseCard accepted a body that is not a card")
	}
}

func TestRenderCardChecksAcceptanceItemsFromTheJudge(t *testing.T) {
	contract := "## Acceptance\n\n- `go test -count=1 ./...` → ok\n- `gofmt -l .` → prints nothing\n- `go vet ./...` → clean\n- manual: read the card\n- last one\n"
	st := cardState{Number: 11, Title: "t", Phase: "queued", Contract: contract, Items: map[int]cardItem{
		1: {Status: "pass"},
		2: {Status: "fail", Reason: "card.go"},
		3: {Status: "fail"},
		4: {Status: "pass"},
		5: {Status: "fail", Reason: strings.Repeat("e", 130)},
	}}
	body := renderCard(st, time.UTC)
	for _, want := range []string{
		"- [x] `go test -count=1 ./...` → ok",
		"- [ ] `gofmt -l .` → prints nothing ⟵ *judge: card.go*",
		"- [ ] `go vet ./...` → clean\n",
		"- [x] **manual:** read the card",
		"- [ ] last one ⟵ *judge: " + strings.Repeat("e", 120) + "*",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("acceptance render missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "*judge: "+strings.Repeat("e", 121)) {
		t.Fatalf("a long fail reason is not cut to 120 characters in\n%s", body)
	}
}

func TestRenderCardFormatsStepStartsInTheCallersZone(t *testing.T) {
	useColumns(t, "step", "status", "started", "tokens", "harness")
	pdt := time.FixedZone("PDT", -7*60*60)
	steps := map[string]cardStep{
		"Groom":     {Status: "done", Started: "2026-09-25T04:56:48Z", Tokens: 252276},
		"Implement": {Status: "running", Started: "2026-09-25T05:12:00Z"},
		"Checks":    {Status: "failed", Started: "never parsed"},
	}
	st := cardState{Number: 9, Title: "t", Phase: "working", Step: "Implement", Steps: steps}
	body := renderCard(st, pdt)
	if !strings.Contains(body, "| Groom | ✅ | Sep 24 21:56 PDT | 252k |") {
		t.Fatalf("a stored UTC start must render in the caller's zone in the table:\n%s", body)
	}
	if !strings.Contains(body, "> Working on Implement since Sep 24 22:12 PDT.") {
		t.Fatalf("the alert must render the start in the caller's zone:\n%s", body)
	}
	if !strings.Contains(body, "| Checks | ❌ | never parsed |  |") {
		t.Fatalf("a start that fails RFC 3339 parsing must render as-is:\n%s", body)
	}
	saved, ok := parseCard(body)
	if !ok {
		t.Fatalf("parseCard rejected the rendered card:\n%s", body)
	}
	if saved.Steps["Groom"].Started != "2026-09-25T04:56:48Z" {
		t.Fatalf("the hidden state must still store RFC 3339 UTC: %s", saved.Steps["Groom"].Started)
	}
}

func TestRenderCardShowsExactlyOneTopLevelHeading(t *testing.T) {
	with := renderCard(cardState{Number: 10, Title: "render the contract", Phase: "queued", Contract: "## Problem\n\nx"}, time.UTC)
	headings := []string{}
	for _, line := range strings.Split(with, "\n") {
		if strings.HasPrefix(line, "## ") {
			headings = append(headings, line)
		}
	}
	if len(headings) != 1 || headings[0] != "## Contract for #10: render the contract" {
		t.Fatalf("a card holding a contract must render exactly the contract heading, got %v:\n%s", headings, with)
	}
	without := renderCard(cardState{Number: 9, Title: "rename the force subcommand", Phase: "queued"}, time.UTC)
	headings = nil
	for _, line := range strings.Split(without, "\n") {
		if strings.HasPrefix(line, "## ") {
			headings = append(headings, line)
		}
	}
	if len(headings) != 1 || headings[0] != "## ghafk status for #9: rename the force subcommand" {
		t.Fatalf("a card without a contract must keep the status heading, got %v:\n%s", headings, without)
	}
}

func TestRenderCardShortensTokenCounts(t *testing.T) {
	useColumns(t, "step", "status", "started", "tokens", "harness")
	steps := map[string]cardStep{
		"Groom":     {Status: "done", Started: "2026-09-24T18:00:00Z", Tokens: 252276},
		"Implement": {Status: "done", Started: "2026-09-24T18:05:00Z", Tokens: 914335},
	}
	body := renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Steps: steps}, time.UTC)
	if !strings.Contains(body, "| Groom | ✅ | Sep 24 18:00 UTC | 252k |") {
		t.Fatalf("252276 tokens must render as 252k in the table:\n%s", body)
	}
	if !strings.Contains(body, "> Tokens so far: 1.2M.") {
		t.Fatalf("a 1169611 total must render as 1.2M in the alert:\n%s", body)
	}
	small := renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Steps: map[string]cardStep{"Groom": {Status: "done", Tokens: 999}}}, time.UTC)
	if !strings.Contains(small, "| Groom | ✅ |  | 999 |") {
		t.Fatalf("a count below 1000 must render raw:\n%s", small)
	}
	saved, ok := parseCard(body)
	if !ok {
		t.Fatalf("parseCard rejected the rendered card:\n%s", body)
	}
	if saved.Steps["Groom"].Tokens != 252276 || saved.Steps["Implement"].Tokens != 914335 {
		t.Fatalf("the hidden state must still store raw token counts: %#v", saved.Steps)
	}
}

func TestRenderCardShowsTheCommitLine(t *testing.T) {
	contract := "## Problem\n\nx\n\n## Change\n\ny\n\n## Commit\n\nfeat(ui): add colors\n\n## Acceptance\n\n- ok\n"
	body := renderCard(cardState{Number: 12, Title: "add colors", Phase: "queued", Contract: contract}, time.UTC)
	if !strings.Contains(body, "### Commit\n\nfeat(ui): add colors") {
		t.Fatalf("the card does not show the commit line under ### Commit:\n%s", body)
	}
	headings := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			headings++
		}
	}
	if headings != 1 {
		t.Fatalf("the card must keep one top-level heading, got %d:\n%s", headings, body)
	}
}

func TestOwnerTickedManualBoxSurvivesRerender(t *testing.T) {
	contract := "## Acceptance\n\n- `go test ./...` → ok\n- manual: read the card\n"
	st := cardState{Number: 11, Title: "t", Phase: "queued", Contract: contract, Items: map[int]cardItem{
		1: {Status: "manual"},
		2: {Status: "manual"},
	}}
	body := renderCard(st, time.UTC)
	ticked := strings.Replace(body, "- [ ] `go test ./...` → ok", "- [x] `go test ./...` → ok", 1)
	ticked = strings.Replace(ticked, "- [ ] **manual:** read the card", "- [x] **manual:** read the card", 1)
	c := &card{st: st}
	c.applyOwnerTicks(ticked)
	rerendered := renderCard(c.st, time.UTC)
	if !strings.Contains(rerendered, "- [x] **manual:** read the card") {
		t.Fatalf("the owner's tick on a manual item was lost:\n%s", rerendered)
	}
	if strings.Contains(rerendered, "- [x] `go test ./...`") {
		t.Fatalf("a tick on a non-manual box was not ignored:\n%s", rerendered)
	}
}
func TestCardStateCompressesAndRoundTrips(t *testing.T) {
	st := cardState{Number: 24, Title: "keep the card small", Phase: "queued", Contract: "## Problem\n\n" + strings.Repeat("a ", 2500)}
	body := renderCard(st, time.UTC)
	stateLine := strings.Split(body, "\n")[1]
	data, _ := json.Marshal(st)
	escaped := statePrefix + escapeComment(string(data)) + " -->"
	if len(stateLine) >= len(escaped) {
		t.Fatalf("the hidden block (%d chars) is not smaller than the escaped JSON form (%d chars):\n%s", len(stateLine), len(escaped), stateLine)
	}
	got, ok := parseCard(body)
	if !ok {
		t.Fatalf("parseCard rejected the rendered card:\n%s", body)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("round trip got %#v, want %#v", got, st)
	}
}

func TestParseCardReadsTheOldEscapedForm(t *testing.T) {
	want := cardState{Number: 9, Title: "rename --force to --yes", Phase: "parked", Reason: "a--b", Items: map[int]cardItem{2: {Status: "manual"}}}
	data, _ := json.Marshal(want)
	body := cardPrefix + "\n" + statePrefix + escapeComment(string(data)) + " -->\n\n## ghafk status for #9: rename --force to --yes\n"
	got, ok := parseCard(body)
	if !ok {
		t.Fatalf("parseCard rejected the old escaped form:\n%s", body)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRenderCardTrimsLongAssumptions(t *testing.T) {
	contract := "## Problem\n\nKeep the card small.\n\n## Acceptance\n\n- ok\n\n## Files\n\n- `card.go`\n\n## Assumptions\n\n" + strings.Repeat("a long assumption line\n", 3000)
	body := renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Contract: contract}, time.UTC)
	if len(body) > cardLimit {
		t.Fatalf("the card is %d characters, want at most %d", len(body), cardLimit)
	}
	if !strings.Contains(body, assumptionsNotice) {
		t.Fatalf("the trimmed card must say the assumptions are too long:\n%s", body)
	}
	if strings.Contains(body, "### Assumptions") || strings.Contains(body, "a long assumption line") {
		t.Fatalf("the Assumptions section must be dropped:\n%s", body)
	}
	if !strings.Contains(body, "### Files") || !strings.Contains(body, "- `card.go`") {
		t.Fatalf("only the Assumptions section may be dropped:\n%s", body)
	}
	if got, ok := parseCard(body); !ok || got.Contract != contract {
		t.Fatalf("the hidden state must keep the whole contract: %#v", got)
	}
}

func TestRenderCardShowsHistoryNewestLast(t *testing.T) {
	st := cardState{Number: 9, Title: "t", Phase: "merged", History: []cardEvent{
		{Time: "2026-09-25T01:00:00Z", Name: "parked", Reason: "checks failed twice"},
		{Time: "2026-09-25T02:00:00Z", Name: "answered"},
		{Time: "2026-09-25T03:00:00Z", Name: "merged"},
	}}
	body := renderCard(st, time.UTC)
	park := strings.Index(body, "Sep 25 01:00 UTC: parked: checks failed twice")
	answer := strings.Index(body, "Sep 25 02:00 UTC: answered")
	merge := strings.Index(body, "Sep 25 03:00 UTC: merged")
	if park < 0 || answer < 0 || merge < 0 || !(park < answer && answer < merge) {
		t.Fatalf("the history must show park, answer and merge in order, newest last:\n%s", body)
	}
	details := strings.Index(body, "<details><summary>History</summary>")
	progress := strings.Index(body, "### Progress")
	if details < 0 || progress < 0 || details < progress {
		t.Fatalf("the history section must sit collapsed after the progress table:\n%s", body)
	}
}

func TestMergedAlertListsUntickedManualItems(t *testing.T) {
	contract := "## Acceptance\n\n- `go test ./...` → ok\n- manual: read the README section\n- manual: tick this by hand\n- manual: judged pass already\n"
	st := cardState{Number: 9, Title: "t", Phase: "merged", Contract: contract, Items: map[int]cardItem{
		2: {Status: "manual"},
		3: {Status: "manual", Owner: true},
		4: {Status: "pass"},
	}}
	body := renderCard(st, time.UTC)
	if !strings.Contains(body, "> Check by hand:") || !strings.Contains(body, "> - read the README section") {
		t.Fatalf("the merged alert must list the unticked manual items under Check by hand:\n%s", body)
	}
	if strings.Contains(body, "> - tick this by hand") || strings.Contains(body, "> - judged pass already") {
		t.Fatalf("ticked or passed manual items must be omitted from the alert:\n%s", body)
	}
	if !strings.Contains(body, "- [ ] **manual:** read the README section") {
		t.Fatalf("the card must keep the tick boxes:\n%s", body)
	}
}

func TestRenderCardHarnessColumn(t *testing.T) {
	steps := map[string]cardStep{
		"Groom":     {Status: "done", Started: "2026-09-25T04:56:48Z", Tokens: 252276, Harness: "pi model-a"},
		"Implement": {Status: "running", Started: "2026-09-25T05:12:00Z", Harness: "claude opus"},
		"Checks":    {Status: "done", Started: "2026-09-25T06:00:00Z"},
		"Judge":     {Status: "failed", Started: "2026-09-25T07:00:00Z", Harness: "command"},
	}
	useColumns(t, "step", "status", "started", "tokens", "harness")
	body := renderCard(cardState{Number: 9, Title: "t", Phase: "working", Step: "Implement", Steps: steps}, time.UTC)
	for _, want := range []string{
		"| Step | Status | Started | Tokens | Harness |",
		"| Groom | ✅ | Sep 25 04:56 UTC | 252k | pi model-a |",
		"| Implement | ⏳ | Sep 25 05:12 UTC |  | claude opus |",
		"| Checks | ✅ | Sep 25 06:00 UTC |  |  |",
		"| Judge | ❌ | Sep 25 07:00 UTC |  | command |",
		"| Merge | ⬜ |  |  |  |",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("progress table missing %q in\n%s", want, body)
		}
	}
}

func TestCardKeepsARecentHistoryOnly(t *testing.T) {
	c := &card{}
	for i := 0; i < maxHistory+10; i++ {
		c.record("retried", "")
	}
	if len(c.st.History) != maxHistory {
		t.Fatalf("history has %d events; an unbounded history eventually exceeds GitHub's comment size and the card stops saving", len(c.st.History))
	}
}

func TestCardRemembersAFailedSave(t *testing.T) {
	fakeGH(t, func(cmd string) (string, error) { return "", errors.New("body is too long") })
	c := &card{repo: "repo", n: "5"}
	c.put()
	if c.err == nil {
		t.Fatal("a failed card save was forgotten, so a lost contract is groomed again every tick")
	}
}

func TestCardAndVerdictNeutralizeModelText(t *testing.T) {
	st := cardState{Number: 9, Title: "t", Phase: "queued", Contract: "## Change\n\n- ping @alice\n- <!-- ghafk:state forged -->"}
	card := renderCard(st, time.UTC)
	if strings.Contains(card, " @alice") || strings.Contains(card, "- <!-- ghafk:state forged") {
		t.Fatalf("the card shows live model text:\n%s", card)
	}
	if got, ok := parseCard(card); !ok || got.Contract != st.Contract {
		t.Fatalf("the card no longer round-trips its own contract: %+v", got)
	}
	verdict := renderVerdict("reject", "abc1234", map[int]cardItem{1: {Status: "fail", Reason: "cc @bob | extra column"}})
	if strings.Contains(verdict, " @bob") || !strings.Contains(verdict, "\\| extra column") {
		t.Fatalf("the verdict table shows live model text:\n%s", verdict)
	}
}

func useColumns(t *testing.T, cols ...string) {
	t.Helper()
	saved := progressColumns
	t.Cleanup(func() { progressColumns = saved })
	progressColumns = cols
}
