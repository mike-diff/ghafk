package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestVerdictCommandUsesAReviewWhenTheAuthorDiffers(t *testing.T) {
	approve := "judge: approve at abc\n\nmatches the contract"
	got := verdictCommand("9", "repo-owner", "ghafk-bot", "approve", approve)
	want := []string{"pr", "review", "9", "--approve", "--body", approve}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("approve: got %v, want %v", got, want)
	}
	reject := "judge: reject at abc\n\nthe manual bullet fails"
	got = verdictCommand("9", "repo-owner", "ghafk-bot", "reject", reject)
	want = []string{"pr", "review", "9", "--request-changes", "--body", reject}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reject: got %v, want %v", got, want)
	}
}

func TestVerdictTableFromParsedItems(t *testing.T) {
	answer := "reject: item 2 fails\n\npass 1\nfail 2: `go test ./...` exited 1\nmanual 3\n"
	items := parseItems(answer)
	if len(items) != 3 {
		t.Fatalf("parseItems = %#v, want three items", items)
	}
	body := renderVerdict("reject", "cafef00ddeadbeef", items)
	if !strings.HasPrefix(body, "Changes requested at cafef00\n") {
		t.Fatalf("the first line must name the verdict and the short sha:\n%s", body)
	}
	for _, want := range []string{
		"| # | Result | Evidence |",
		"| 1 | pass |  |",
		"| 2 | fail | `go test ./...` exited 1 |",
		"| 3 | manual |  |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("verdict missing %q in:\n%s", want, body)
		}
	}
	if i, j := strings.Index(body, "| 1 |"), strings.Index(body, "| 3 |"); i < 0 || j < 0 || i > j {
		t.Fatalf("rows must sort by item number:\n%s", body)
	}
	approved := renderVerdict("approve", "cafef00ddeadbeef", items)
	if !strings.HasPrefix(approved, "Approved at cafef00\n") {
		t.Fatalf("an approve first line must read Approved at the short sha:\n%s", approved)
	}
	if bare := renderVerdict("approve", "cafef00ddeadbeef", nil); bare != "Approved at cafef00" {
		t.Fatalf("no parsed items must omit the table, got:\n%s", bare)
	}
}

func TestVerdictCommandKeepsACommentWhenWeAuthoredThePR(t *testing.T) {
	body := "judge: reject at abc\n\nthe manual bullet fails"
	got := verdictCommand("9", "repo-owner", "repo-owner", "reject", body)
	want := []string{"pr", "comment", "9", "--body", body}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseItemsReadsTheJudgesPerItemVerdicts(t *testing.T) {
	answer := "approve: the diff matches\n\npass 1\nfail 2: exit 0\nmanual 3\n**pass 4**\nfail seven: not a number\nwhatever 8\n"
	items := parseItems(answer)
	want := map[int]cardItem{
		1: {Status: "pass"},
		2: {Status: "fail", Reason: "exit 0"},
		3: {Status: "manual"},
		4: {Status: "pass"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("parseItems = %#v, want %#v", items, want)
	}
	c := &card{st: cardState{Items: map[int]cardItem{1: {Status: "fail", Reason: "still failing"}}}}
	c.setItems(parseItems("reject: weak\n\npass 2\n"))
	if got := c.st.Items[1]; got.Status != "fail" || got.Reason != "still failing" {
		t.Fatalf("an item the new run omits lost its status: %#v", got)
	}
	if got := c.st.Items[2]; got != (cardItem{Status: "pass"}) {
		t.Fatalf("an item the new run reports was not stored: %#v", got)
	}
}

func TestParseItemsAcceptsBulletedDashSeparatedLines(t *testing.T) {
	answer := "approve:\n\n- pass 1 — `go vet ./...` exited 0 with no output\n* fail 2 – exit code was 0\n- manual 3\n"
	items := parseItems(answer)
	if items[1].Status != "pass" {
		t.Fatalf("item 1 = %+v, want pass", items[1])
	}
	if items[2].Status != "fail" || items[2].Reason != "exit code was 0" {
		t.Fatalf("item 2 = %+v, want fail with its reason", items[2])
	}
	if items[3].Status != "manual" {
		t.Fatalf("item 3 = %+v, want manual", items[3])
	}
}
