package main

import (
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		body string
		verb string
		text string
	}{
		{"/answer pick 1", "answer", "pick 1"},
		{"/Answer  pick 1", "answer", "pick 1"},
		{"/answer", "", ""},
		{"/RETRY", "retry", ""},
		{"/close", "close", ""},
		{"/stop", "stop", ""},
		{"/start", "start", ""},
		{"/help", "help", ""},
		{"start this", "", ""},
		{"  /stop  ", "stop", ""},
		{"/retrying", "", ""},
		{"stop it, really", "", ""},
		{"close to done, needs tests", "", ""},
		{"retry", "", ""},
		{"answer: pick 1", "", ""},
		{"please /retry later", "", ""},
		{"> /answer pick 1", "", ""},
		{"looks reasonable to me", "", ""},
	}
	for _, want := range cases {
		if verb, text := parseCommand(want.body); verb != want.verb || text != want.text {
			t.Errorf("parseCommand(%q) = (%q, %q), want (%q, %q)", want.body, verb, text, want.verb, want.text)
		}
	}
}

func TestNewestCommand(t *testing.T) {
	sharedLogin = false
	defer func() { sharedLogin = true }()
	me := "ghafk-bot"
	mine := prComment{Body: "/retry", Author: author{Login: me}}
	mimic := prComment{Body: "needs-human: retry the palette", Author: author{Login: "repo-owner"}}
	older := prComment{Body: "/close", Author: author{Login: "repo-owner"}, Association: "OWNER"}
	newer := prComment{Body: "/stop", Author: author{Login: "repo-owner"}, Association: "OWNER"}
	if c := newestCommand([]prComment{mine}, me); c != nil {
		t.Fatalf("a comment by the engine's own login starting with retry is not a command, got %q", c.Body)
	}
	if c := newestCommand([]prComment{mimic}, me); c != nil {
		t.Fatalf("a comment carrying an engine prefix is not a command, got %q", c.Body)
	}
	if c := newestCommand([]prComment{older, newer}, me); c == nil || c.Body != newer.Body {
		t.Fatal("only the newest command comment counts")
	}
}

func TestParkedByUs(t *testing.T) {
	me, owner := "ghafk-bot", "repo-owner"
	park := prComment{Body: parkMarker + "\nThe checks failed twice.", Author: author{Login: me}}
	markedPark := prComment{Body: renderComment(commentSpec{kind: "park", role: "checks", number: 56, sentence: "The checks failed twice."}), Author: author{Login: me}}
	remark := prComment{Body: "any news on this?", Author: author{Login: owner}}
	retry := prComment{Body: "/retry", Author: author{Login: owner}, Association: "OWNER"}
	if !parkedByUs([]prComment{park}, me) {
		t.Fatal("a PR whose newest comment is our needs-human is parked")
	}
	if !parkedByUs([]prComment{markedPark}, me) {
		t.Fatal("a park comment carrying the marker parks")
	}
	if !parkedByUs([]prComment{park, remark}, me) {
		t.Fatal("a plain remark leaves the PR parked")
	}
	if parkedByUs([]prComment{park, retry}, me) {
		t.Fatal("a retry command unparks the PR")
	}
	if parkedByUs([]prComment{markedPark, retry}, me) {
		t.Fatal("a retry command unparks a marker park")
	}
	if !parkedByUs([]prComment{park, retry, park}, me) {
		t.Fatal("a park after the retry parks the PR again")
	}
}

func TestOwnerCommandsCountWhenEngineSharesTheLogin(t *testing.T) {
	stop := prComment{Author: author{Login: "owner"}, Body: "/stop"}
	card := prComment{Author: author{Login: "owner"}, Body: "<!-- ghafk:card -->\nstatus"}
	defer func() { sharedLogin = true }()
	sharedLogin = true
	if engineWrote(stop, "owner") {
		t.Fatal("a plain owner comment was taken as the engine's own while engine and owner share a login")
	}
	if !engineWrote(card, "owner") {
		t.Fatal("a marked engine comment was not recognized")
	}
	sharedLogin = false
	if !engineWrote(stop, "owner") {
		t.Fatal("with a separate engine account, its comments are recognized by author")
	}
}

func TestCommandsCountOnlyFromTrustedAuthors(t *testing.T) {
	stubWriters(t, "someone")
	for association, want := range map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true, "CONTRIBUTOR": false, "FIRST_TIME_CONTRIBUTOR": false, "NONE": false, "": false} {
		c := prComment{Body: "/start", Author: author{Login: "someone"}, Association: association}
		if got := newestCommand([]prComment{c}, "owner") != nil; got != want {
			t.Errorf("/start from %q counted = %v, want %v", association, got, want)
		}
	}
}

func TestStartLabelsAnIdleIssue(t *testing.T) {
	cases := []struct {
		name            string
		labeled, parked bool
		want            bool
	}{
		{"unlabeled or stopped issue", false, false, true},
		{"parked issue", false, true, true},
		{"already being worked", true, false, false},
	}
	for _, c := range cases {
		if got := startApplies(commandTarget{labeled: c.labeled, parked: c.parked}); got != c.want {
			t.Errorf("%s: startApplies = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestARetryOlderThanTheLatestParkIsIgnored(t *testing.T) {
	stubWriters(t)
	calls := fakeGH(t, nil)
	target := &commandTarget{number: 4, parked: true, comments: []prComment{
		{Body: "/retry", Author: author{Login: "owner"}, Association: "OWNER", CreatedAt: "2026-09-26T10:00:00Z", URL: "https://github.com/o/r/issues/4#issuecomment-1"},
		{Body: parkMarker + "\nThe checks failed twice.", Author: author{Login: "ghafk"}, CreatedAt: "2026-09-26T10:05:00Z"},
	}}
	steerIssue("repo", "demo", "ghafk", workflow{label: "agent"}, target)
	if called(*calls, "issue edit 4 --add-label agent") {
		t.Fatalf("a /retry from before the latest park requeued the issue, so it can loop every tick: %v", *calls)
	}
}

func TestHelpRepliesWithCommandsAndLabels(t *testing.T) {
	stubWriters(t)
	var posted string
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "issue comment 12 ") {
			posted = cmd
		}
		return "", nil
	})
	target := &commandTarget{number: 12, comments: []prComment{
		{Body: "/help", Author: author{Login: "owner"}, Association: "OWNER", URL: "https://github.com/o/r/issues/12#issuecomment-7"},
	}}
	if steerIssue("repo", "demo", "ghafk", workflow{label: "work"}, target) {
		t.Fatal("a /help command must not hold the issue")
	}
	if posted == "" {
		t.Fatalf("no reply comment was posted for /help: %v", *calls)
	}
	for _, want := range []string{"`/start`", "`/answer <text>`", "`/retry`", "`/close`", "`/stop`", "`work`", "`needs-human`"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the /help reply lacks %s: %s", want, posted)
		}
	}
	for _, banned := range []string{"issue edit", "issue close"} {
		if called(*calls, banned) {
			t.Errorf("/help must not change labels or close the issue: %v", *calls)
		}
	}
	if !called(*calls, "api -X POST repos/{owner}/{repo}/issues/comments/7/reactions") {
		t.Errorf("/help must get a reaction on the command comment: %v", *calls)
	}
}

func TestSteerFindsLabeledIssuesBeyondAnOutsiderFlood(t *testing.T) {
	stubWriters(t)
	calls := fakeGH(t, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "issue list") && strings.Contains(cmd, "--label agent") {
			return `[{"number":9,"title":"t","body":"b","labels":[{"name":"agent"}],"comments":[{"body":"/stop","author":{"login":"owner"},"authorAssociation":"OWNER","url":"https://github.com/o/r/issues/9#issuecomment-1","createdAt":"2026-09-26T10:00:00Z"}]}]`, nil
		}
		if strings.HasPrefix(cmd, "issue list") || strings.HasPrefix(cmd, "pr list") {
			return "[]", nil
		}
		return "", nil
	})
	if _, err := steer("repo", "demo", "ghafk", workflow{label: "agent"}); err != nil {
		t.Fatal(err)
	}
	if !called(*calls, "issue edit 9 --remove-label agent") {
		t.Fatalf("a /stop on a labeled issue was missed when the open-issue list was full of other issues: %v", *calls)
	}
}

func TestListPRsAsksGitHubForTheOwnersPRsOnly(t *testing.T) {
	ownerLogin = "owner"
	calls := fakeGH(t, func(string) (string, error) { return "[]", nil })
	if _, err := listPRs("repo"); err != nil {
		t.Fatal(err)
	}
	if !called(*calls, "pr list --state open --author owner") {
		t.Fatalf("PRs are filtered after a 500-item list, so 500 fork PRs hide the engine's own: %v", *calls)
	}
}
