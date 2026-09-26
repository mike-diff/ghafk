package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSectionLines(t *testing.T) {
	doc := "## Problem\np\n## Files\n- `a.go`\n### Note\nn\n- `b.go`\n## Commit\nfix: x\n"
	if got := strings.Join(sectionLines(doc, "## ", "Files"), "|"); got != "- `a.go`|### Note|n|- `b.go`" {
		t.Fatalf("Files section: got %q; a deeper heading stays inside the section", got)
	}
	if got := strings.Join(sectionLines(doc, "## ", "Commit"), "|"); got != "fix: x|" {
		t.Fatalf("last section runs to the end: got %q", got)
	}
	if got := sectionLines(doc, "## ", "Missing"); got != nil {
		t.Fatalf("missing section: got %q, want nil", got)
	}
	if got := strings.Join(sectionLines(doc, "### ", "Note"), "|"); got != "n|- `b.go`" {
		t.Fatalf("### level stops at a ## heading: got %q", got)
	}
}

func TestContractTextReadsOnlyTheCard(t *testing.T) {
	me := "ghafk-bot"
	plain := prComment{Body: "contract:\n\n## Problem\n\nplain text", Author: author{Login: me}}
	withContract := prComment{Body: renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Contract: "## Problem\n\ncard text"}, time.UTC), Author: author{Login: me}}
	midGroom := prComment{Body: renderCard(cardState{Number: 9, Title: "t", Phase: "working", Step: "Groom"}, time.UTC), Author: author{Login: me}}
	if got := contractText([]prComment{plain, withContract}, me); got != "## Problem\n\ncard text" {
		t.Fatalf("contractText = %q, want the card's contract", got)
	}
	if got := contractText([]prComment{plain, midGroom}, me); got != "" {
		t.Fatalf("contractText = %q; a plain comment is not a contract and a card without one adds none", got)
	}
}

func TestAnswerFromSkipsNarrationAndDecoration(t *testing.T) {
	out := "Confirmed the shape.\n\n`contract:`\n\n## Problem\nx"
	got := answerFrom(out, "contract:", "question:")
	if got != "contract:\n\n## Problem\nx" {
		t.Fatalf("got %q", got)
	}
}

func TestAnswerFromWithoutMarkerReturnsWholeOutput(t *testing.T) {
	if got := answerFrom("  nothing here \n", "approve:", "reject:"); got != "nothing here" {
		t.Fatalf("got %q", got)
	}
}

func TestAnswerFromStripsDecorationAfterTheMarker(t *testing.T) {
	out := "Sure.\n`reject:` the manual bullet fails"
	if got := answerFrom(out, "approve:", "reject:"); got != "reject: the manual bullet fails" {
		t.Fatalf("got %q, want no stray backtick after the marker", got)
	}
}

func TestValidCommitSubject(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
	}{
		{"feat(app): add colors", true},
		{"feat: add colors", true},
		{"feat: " + strings.Repeat("a", 66), true},
		{"feat: Add colors", false},
		{"feat: add colors.", false},
		{"feat: add colors (#58)", false},
		{"feat: close #33 now", false},
		{"wip: add colors", false},
		{"feat: " + strings.Repeat("a", 67), false},
		{"feat(Pt): add colors", false},
		{"feat(pt2-ui): add colors", true},
		{"feat(): add colors", false},
		{"feat:add colors", false},
		{"feat: 3 colors", false},
		{"feat: ", false},
		{"", false},
	}
	for _, want := range cases {
		if got := validCommitSubject(want.line); got != want.ok {
			t.Errorf("validCommitSubject(%q) = %v, want %v", want.line, got, want.ok)
		}
	}
}

func TestFallbackCommitSubject(t *testing.T) {
	title := "Add " + strings.Repeat("colors to the palette ", 3) + "everywhere"
	if len(title) != 80 {
		t.Fatalf("test title is %d characters, want 80", len(title))
	}
	got := fallbackCommitSubject(title)
	if n := len([]rune(got)); n > 72 {
		t.Fatalf("fallback from an 80-character title is %d characters, want at most 72: %q", n, got)
	}
	if !validCommitSubject(got) {
		t.Fatalf("fallback %q is not a valid subject", got)
	}
	if got := fallbackCommitSubject("Post grade changes now."); got != "feat: post grade changes now" {
		t.Fatalf("fallback = %q, want the trailing period dropped and a lowercase start", got)
	}
	if got := fallbackCommitSubject("Drop  the #33 duplicate and  #7 tag"); got != "feat: drop the duplicate and tag" {
		t.Fatalf("fallback = %q, want issue numbers removed and whitespace collapsed", got)
	}
	hard := fallbackCommitSubject(strings.Repeat("A", 90))
	if len([]rune(hard)) != 72 || !validCommitSubject(hard) {
		t.Fatalf("a title with no space must cut hard at 72, got %q", hard)
	}
}

func TestCommitSubject(t *testing.T) {
	contract := "## Problem\n\nx\n\n## Change\n\ny\n\n## Commit\n\nfeat(ui): add colors\n\na second line\n\n## Acceptance\n\n- ok\n"
	if got := commitSubject(contract, "Add colors"); got != "feat(ui): add colors" {
		t.Fatalf("commitSubject = %q, want the contract's commit line", got)
	}
	broken := strings.Replace(contract, "feat(ui): add colors", "feat(ui): Add colors", 1)
	if got := commitSubject(broken, "Add colors"); got != "feat: add colors" {
		t.Fatalf("commitSubject = %q, want the fallback when the line breaks the rules", got)
	}
	if got := commitSubject("## Problem\n\nx", "Add colors"); got != "feat: add colors" {
		t.Fatalf("commitSubject = %q, want the fallback when there is no commit section", got)
	}
}

func TestStaleAfterContractTreatsACardHoldingAContract(t *testing.T) {
	me := "ghafk-bot"
	stale := prComment{Body: reconcileStaleMarker + "\nstale at abc", Author: author{Login: me}}
	markedStale := prComment{Body: renderComment(commentSpec{kind: "reconcile-stale", role: "reconciler", number: 9, sentence: "The reconciler marked the contract stale at abc."}), Author: author{Login: me}}
	withContract := prComment{Body: renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Contract: "## Problem\n\nx"}, time.UTC), Author: author{Login: me}}
	midGroom := prComment{Body: renderCard(cardState{Number: 9, Title: "t", Phase: "working", Step: "Groom"}, time.UTC), Author: author{Login: me}}
	if !staleAfterContract([]prComment{withContract, stale}, me) {
		t.Fatal("a stale verdict after a card holding a contract must trigger a re-groom")
	}
	if !staleAfterContract([]prComment{withContract, markedStale}, me) {
		t.Fatal("a marker stale verdict after a card holding a contract must trigger a re-groom")
	}
	if staleAfterContract([]prComment{stale, withContract}, me) {
		t.Fatal("a card holding a contract after the stale verdict must not")
	}
	if !staleAfterContract([]prComment{stale, midGroom}, me) {
		t.Fatal("a card without a contract must not reset the stale verdict")
	}
}

func TestContractFilesStopsAtNextHeading(t *testing.T) {
	contract := "## Change\nx\n\n## Files\n\n- `cmd/app/main.go`\n- internal/db/ (new table)\n\n## Assumptions\n- `not/a/file.go`\n"
	got := contractFiles(contract)
	want := []string{"cmd/app/main.go", "internal/db/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOverlapsMatchesUnderAListedDirectory(t *testing.T) {
	if !overlaps([]string{"internal/db/"}, []string{"internal/db/schema.sql"}) {
		t.Fatal("a landed file under a listed directory must overlap")
	}
	if overlaps([]string{"internal/db"}, []string{"internal/dbx/x.go"}) {
		t.Fatal("a bare prefix without a slash must not overlap")
	}
}

func TestStaleAfterContractDependsOnOrder(t *testing.T) {
	me := "ghafk-bot"
	stale := prComment{Body: reconcileStaleMarker + "\nstale at abc", Author: author{Login: me}}
	contract := prComment{Body: renderCard(cardState{Number: 9, Title: "t", Phase: "queued", Contract: "## Problem"}, time.UTC), Author: author{Login: me}}
	if !staleAfterContract([]prComment{contract, stale}, me) {
		t.Fatal("a stale verdict after the contract must trigger a re-groom")
	}
	if staleAfterContract([]prComment{stale, contract}, me) {
		t.Fatal("a contract written after the stale verdict must not")
	}
}

func TestCommitLineInsideACodeFenceIsRead(t *testing.T) {
	contract := "## Change\n\n- x\n\n## Commit\n\n```\ndocs(readme): note that app version exits 0\n```\n\n## Acceptance\n"
	if got := commitSubject(contract, "README: mention that app version exits 0"); got != "docs(readme): note that app version exits 0" {
		t.Fatalf("fenced commit line was skipped for the title fallback: %q", got)
	}
	inline := "## Commit\n\n`fix(pr): keep the label`\n"
	if got := commitSubject(inline, "t"); got != "fix(pr): keep the label" {
		t.Fatalf("backticks around the commit line were kept or it was skipped: %q", got)
	}
}

func TestFallbackKeepsAnAcronymAtTheStart(t *testing.T) {
	if got := fallbackCommitSubject("README: mention that app version exits 0"); got != "feat: readme: mention that app version exits 0" {
		t.Fatalf("fallback mangled the leading acronym instead of lowercasing it whole: %q", got)
	}
	if got := fallbackCommitSubject("HTTPServer timeouts"); got != "feat: httpServer timeouts" {
		t.Fatalf("fallback lowercased past the acronym into the next word: %q", got)
	}
	if got := fallbackCommitSubject("Add a flag"); got != "feat: add a flag" {
		t.Fatalf("fallback no longer lowercases an ordinary first word: %q", got)
	}
}
