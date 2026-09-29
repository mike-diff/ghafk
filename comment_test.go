package main

import (
	"github.com/mike-diff/ghafk/internal/harness"

	"bytes"
	"errors"
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
	needSandbox(t)
	var buf bytes.Buffer
	err := runSandboxed(sandboxOpts{name: "groomer", dir: t.TempDir(), command: "sleep 30", timeout: 50 * time.Millisecond, role: &harness.Role{Command: "sh"}, home: t.TempDir()}, &buf, &buf)
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

func TestAgentTextInCardsAndQuestionsCannotMention(t *testing.T) {
	card := renderCard(cardState{Number: 5, Title: "@alice fix it", Phase: "parked", Reason: "@bob asked", History: []cardEvent{{Time: "2026-09-26T10:00:00Z", Name: "parked", Reason: "@carol asked"}}}, time.UTC)
	question := questionBody("ask @dave?", []string{"@erin"})
	for _, who := range []string{"alice", "bob", "carol"} {
		if !strings.Contains(card, "`@"+who+"`") {
			t.Errorf("the card leaves @%s live, so the bot pings a user that agent text named:\n%s", who, card)
		}
	}
	for _, who := range []string{"dave", "erin"} {
		if !strings.Contains(question, "`@"+who+"`") {
			t.Errorf("the question leaves @%s live:\n%s", who, question)
		}
	}
}

func TestNeutralizeCoversURLClosingRefsAndLoneBackticks(t *testing.T) {
	if got := neutralize("Fixes https://github.com/o/r/issues/7"); got != "Fixes `https://github.com/o/r/issues/7`" {
		t.Errorf("a closing keyword with an issue URL stayed live: %q", got)
	}
	if got := neutralize("see `@octocat for details"); !strings.Contains(got, "`@octocat`") {
		t.Errorf("a lone backtick kept @octocat outside a code span: %q", got)
	}
	if got := neutralize(neutralize("hi @octocat")); got != "hi `@octocat`" {
		t.Errorf("neutralize is not stable when applied twice: %q", got)
	}
	if got := neutralize("mail a@octocat.com"); got != "mail a@octocat.com" {
		t.Errorf("an email address was changed: %q", got)
	}
}

func TestParkedErrorsCannotCloseTheirFence(t *testing.T) {
	calls := fakeGH(t, nil)
	at := parkPlace{repo: "repo", base: "demo", login: "owner", label: "agent", target: "5", issue: 5}
	if err := parkOnError(at, "checks", "The checks failed.", errors.New("out\n```\n@octocat <img src=x>")); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		for i, a := range c.args {
			if a == "--body" && i+1 < len(c.args) && strings.Contains(c.args[i+1], "\n```\n@octocat") && !strings.Contains(c.args[i+1], "````") {
				t.Fatalf("error text closed the code fence, so the rest renders as markdown:\n%s", c.args[i+1])
			}
		}
	}
}

func TestBlockedHostsReachTheParkComment(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	err := fmt.Errorf("worker: %w"+deniedMarker+"%s", exitErr, "blocked.example.test")
	if _, body := runFailure("worker", err, time.Minute); !strings.Contains(body, "blocked.example.test") {
		t.Fatalf("the park body does not name the blocked host: %q", body)
	}
	if text := failureEvidence("make test", err, "FAIL x", time.Minute); !strings.Contains(text, "blocked.example.test") {
		t.Fatalf("the checks evidence does not name the blocked host: %q", text)
	}
}

func TestParkCommentsShowPathsUnderTheHomeAsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	calls := fakeGH(t, nil)
	err := errors.New("the program " + home + "/tools/x needs " + home + "/.ssh")
	_ = parkOnError(parkPlace{repo: "repo", base: "demo", login: "owner", label: "agent", target: "5", issue: 5}, "worker", "The worker failed.", err)
	if _, body := runFailure("worker", err, time.Minute); strings.Contains(body, home) {
		t.Fatalf("a run failure posted the local home path: %s", body)
	}
	for _, c := range *calls {
		if strings.Contains(c.String(), home) {
			t.Fatalf("a park comment posted the local home path: %s", c)
		}
		if strings.Contains(c.String(), "~/tools/x") {
			return
		}
	}
	t.Fatalf("the park comment does not show the path under ~: %v", *calls)
}
