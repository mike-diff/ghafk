package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParkEvidenceShowsFailingLinesFromTheMiddle(t *testing.T) {
	runErr := fmt.Errorf("checks: %w", exec.Command("sh", "-c", "exit 3").Run())
	var out strings.Builder
	for i := 0; i < 10; i++ {
		out.WriteString("ok   example.com/pkg/early\t0.10s\n")
	}
	out.WriteString("--- FAIL: TestMidOutput (0.00s)\n")
	out.WriteString("    mid_test.go:12: error: want 1, got 2\n")
	out.WriteString("FAIL\n")
	for i := 0; i < 40; i++ {
		out.WriteString("ok   example.com/pkg/late\t0.10s\n")
	}
	spec := commentSpec{kind: "park", role: "checks", number: 53, sentence: "The checks failed twice.", body: failureEvidence("go test ./...", runErr, out.String(), checkTimeout), footer: retryFooter()}
	rendered := renderComment(spec)
	for _, want := range []string{
		"<!-- ghafk:park -->",
		"> [!IMPORTANT]",
		"> **ghafk** · checks · #53",
		"> The checks failed twice.",
		"`go test ./...` exited with code 3",
		"<summary>Evidence</summary>",
		"--- FAIL: TestMidOutput (0.00s)",
		"mid_test.go:12: error: want 1, got 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("park missing %q in:\n%s", want, rendered)
		}
	}
}

func TestQuestionRendersNumberedOptionsWithARecommendedOne(t *testing.T) {
	answer := "question: Which palette should `app` use by default?\n1. Monochrome only (recommended)\n2) Monochrome plus a `--color` flag\n3. Full 16-color output"
	question, options := parseQuestion(strings.TrimPrefix(answer, "question:"))
	if question != "Which palette should `app` use by default?" {
		t.Fatalf("question = %q", question)
	}
	if len(options) != 3 {
		t.Fatalf("options = %v, want three", options)
	}
	spec := commentSpec{kind: "question", role: "groomer", number: 39, sentence: "The groomer needs your answer.", body: questionBody(question, options), footer: questionFooter()}
	rendered := renderComment(spec)
	for _, want := range []string{
		"> [!IMPORTANT]",
		"> **ghafk** · groomer · #39",
		"1. Monochrome only (recommended)",
		"2. Monochrome plus a `--color` flag",
		"3. Full 16-color output",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("question missing %q in:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "2)") {
		t.Errorf("the parenthesized option number must be renumbered:\n%s", rendered)
	}
}

func TestQuestionWithoutOptionsRendersTheQuestionAlone(t *testing.T) {
	question, options := parseQuestion("Which name do you want: `app force` or `app apply`?")
	if len(options) != 0 {
		t.Fatalf("options = %v, want none", options)
	}
	if got := questionBody(question, options); got != "Which name do you want: `app force` or `app apply`?" {
		t.Fatalf("body = %q", got)
	}
}

func TestKilledRunRendersTheTimeoutSentence(t *testing.T) {
	var buf bytes.Buffer
	err := runShell("groomer", ".", "sleep 30", "", 50*time.Millisecond, &buf, &buf)
	if err == nil {
		t.Fatal("the timed-out run must return an error")
	}
	sentence, body := runFailure("groomer", err, 30*time.Minute)
	if sentence != "The groomer ran longer than the 30 minute limit and was stopped." {
		t.Fatalf("sentence = %q", sentence)
	}
	if body != "" {
		t.Fatalf("a stopped run carries no detail block, got %q", body)
	}
}

func TestPlainExitRendersTheExitSentence(t *testing.T) {
	err := fmt.Errorf("worker: %w", exec.Command("sh", "-c", "exit 2").Run())
	sentence, body := runFailure("worker", err, 30*time.Minute)
	if sentence != "The worker exited with code 2." {
		t.Fatalf("sentence = %q", sentence)
	}
	if body != "" {
		t.Fatalf("a plain exit carries no detail block, got %q", body)
	}
}

func TestParkEqualToThePreviousEngineCommentIsNotPosted(t *testing.T) {
	me, owner := "ghafk-bot", "repo-owner"
	spec := commentSpec{kind: "park", role: "checks", number: 56, sentence: "The checks failed twice.", body: "The command `go test ./...` exited with code 1.", footer: retryFooter()}
	body := renderComment(spec)
	prior := []prComment{{Body: body, Author: author{Login: me}}}
	if !parkDupe(prior, me, body) {
		t.Fatal("a park equal to the engine's previous comment must not post")
	}
	other := renderComment(commentSpec{kind: "park", role: "checks", number: 56, sentence: "The checks failed twice.", body: "The command `go vet ./...` exited with code 1.", footer: retryFooter()})
	if parkDupe(prior, me, other) {
		t.Fatal("a park that differs from the previous engine comment must post")
	}
	withRemark := []prComment{prior[0], {Body: "any news on this?", Author: author{Login: owner}}}
	if !parkDupe(withRemark, me, body) {
		t.Fatal("the newest engine comment, not the newest comment, is the comparison")
	}
}

func TestEmDashScrubLeavesBacktickSpansAlone(t *testing.T) {
	in := "The old flag — keep `--x — y` — is gone—today"
	want := "The old flag, keep `--x — y`, is gone, today"
	if got := scrubEmDashes(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderCommentKeepsVerdictsAsNotesWithoutFooter(t *testing.T) {
	spec := commentSpec{kind: "verdict", role: "judge", number: 44, sentence: "The judge approved the diff at abc.", body: "every acceptance command passes"}
	rendered := renderComment(spec)
	for _, want := range []string{"<!-- ghafk:verdict -->", "> [!NOTE]", "> **ghafk** · judge · #44"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("verdict missing %q in:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "[!IMPORTANT]") || strings.Contains(rendered, "Reply ") {
		t.Fatalf("a verdict is a note without a footer:\n%s", rendered)
	}
}

func TestEngineCommentRecognizesTheCard(t *testing.T) {
	if !engineComment("<!-- ghafk:card -->\n<!-- ghafk:state {\"number\":9} -->\n\n## ghafk status for #9") {
		t.Fatal("the status card is the engine's own comment")
	}
	if engineComment("please use --force") {
		t.Fatal("an author comment containing -- is not the engine's")
	}
}

func TestParkFooter(t *testing.T) {
	sep := "\n\n---\n\n"
	if got := questionFooter(); got != sep+"Reply `/answer <your choice>`. ghafk continues on the next tick.\nOther commands: `/retry` · `/stop`" {
		t.Errorf("groom question footer = %q", got)
	}
	if got := retryFooter(); got != sep+"Reply `/retry`. ghafk continues on the next tick.\nOther commands: `/close` · `/stop`" {
		t.Errorf("failed-checks footer = %q", got)
	}
	if got := closeFooter(); got != sep+"Reply `/close`. ghafk closes the issue as completed.\nOther commands: `/retry` · `/stop`" {
		t.Errorf("reconcile done footer = %q", got)
	}
}

func TestEngineCommentIsMarkerOnly(t *testing.T) {
	for _, body := range []string{"<!-- ghafk:park -->\n> [!IMPORTANT]", "<!-- ghafk:note -->\n> [!NOTE]"} {
		if !engineComment(body) {
			t.Errorf("engineComment(%q) = false, want true", body)
		}
	}
	for _, body := range []string{"judge: approve at abc", "contract:\n\n## Problem", "needs-human: checks failed twice", "please use --force"} {
		if engineComment(body) {
			t.Errorf("engineComment(%q) = true; plain text anyone can type must not pass as the engine's", body)
		}
	}
}
