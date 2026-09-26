package main

import (
	"strings"
	"testing"

	"github.com/mike-diff/ghafk/internal/harness"
)

func TestRenderPRBodySections(t *testing.T) {
	output := "I read the issue and made the change.\n\nThe renderer writes every section.\nTests cover each one.\n\nusage: 230672 tokens $0.1600"
	answer, _ := harness.SplitUsage(output)
	stat := " main.go    | 12 +++++---\n prbody.go | 95 +++++++++++\n 2 files changed, 100 insertions(+), 4 deletions(-)"
	st := prState{Checks: "pass", ChecksSha: "be32f59abcdef0123", Verdict: "approve", VerdictSha: "be32f59abcdef0123", Repairs: 1}
	body := renderPRBody(22, summaryLines(answer), stat, st)
	lines := strings.Split(body, "\n")
	if lines[0] != "Closes #22" {
		t.Fatalf("first line = %q, want Closes #22", lines[0])
	}
	for _, want := range []string{
		"## Summary\n\nThe renderer writes every section.\nTests cover each one.",
		"## Changes\n\n```\n" + stat + "\n```",
		"| Step | Result | At |",
		"| Checks | pass | be32f59 |",
		"| Judge | approve | be32f59 |",
		"| Repairs | 1 |  |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
	last := -1
	for _, marker := range []string{"Closes #22", "## Summary", "## Changes", "## Status", prStatePrefix} {
		i := strings.Index(body, marker)
		if i < 0 || i < last {
			t.Fatalf("section %q missing or out of order in:\n%s", marker, body)
		}
		last = i
	}
	if !strings.HasPrefix(lines[len(lines)-1], prStatePrefix) {
		t.Fatalf("last line is not the state block:\n%s", body)
	}
	if _, ok := parsePRState(body); !ok {
		t.Fatal("the rendered body must parse back into a state")
	}
	capped := summaryLines("lead\n\n1\n2\n3\n4\n5\n6\n7\n8")
	if !strings.HasPrefix(capped, "4") || !strings.HasSuffix(capped, "8") || strings.Count(capped, "\n") != 4 {
		t.Fatalf("summary must keep the last five non-empty lines, got:\n%s", capped)
	}
}

func TestPRStateRoundTripsThroughBody(t *testing.T) {
	st := prState{Checks: "fail", ChecksSha: "0123456789abcdef", Verdict: "reject", VerdictSha: "fedcba9876543210", Repairs: 3}
	body := renderPRBody(9, "kept summary", " main.go | 1 +", st)
	got, ok := parsePRState(body)
	if !ok {
		t.Fatalf("parsePRState rejected the rendered body:\n%s", body)
	}
	if got != st {
		t.Fatalf("round trip got %#v, want %#v", got, st)
	}
	if bodySummary(body) != "kept summary" {
		t.Fatalf("bodySummary = %q, want the section text kept", bodySummary(body))
	}
	if _, ok := parsePRState("Closes #9\n\nno state block"); ok {
		t.Fatal("a body without a state block must not parse")
	}
}

func TestRenderPRBodyScrubsSummaryDashes(t *testing.T) {
	summary := "Fix the stat join — and keep `a—b` as written"
	body := renderPRBody(23, summary, " main.go | 1 +", prState{})
	if !strings.Contains(body, "Fix the stat join, and keep `a—b` as written") {
		t.Fatalf("summary dash outside backticks must become a comma, in:\n%s", body)
	}
	if !strings.Contains(body, "`a—b`") || strings.Contains(body, " — ") {
		t.Fatalf("backticked dash must survive and spaced dash must go, in:\n%s", body)
	}
}

func TestBodySummaryKeepsTheExistingSummary(t *testing.T) {
	current := renderPRBody(9, "first summary", " main.go | 1 +", prState{Checks: "fail", ChecksSha: "abc"})
	if got := bodySummary(current); got != "first summary" {
		t.Fatalf("bodySummary = %q, want first summary", got)
	}
	if got := bodySummary("Closes #9\n\nopened before this change"); got != "" {
		t.Fatalf("a body with no sections has no summary, got %q", got)
	}
}
