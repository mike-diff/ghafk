package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var commandEvent = map[string]string{"answer": "answered", "retry": "retried"}

type commandTarget struct {
	number   int
	title    string
	body     string
	comments []prComment
	labeled  bool
	parked   bool
}

func startApplies(t commandTarget) bool {
	return t.parked || !t.labeled
}

func steer(repo, base, login string, wf workflow) (map[int]bool, error) {
	held := map[int]bool{}
	if err := ensureNeedsHumanLabel(repo); err != nil {
		return held, err
	}
	targets := map[int]*commandTarget{}
	var order []int
	var open []issue
	if err := ghJSON(repo, []string{"issue", "list", "--state", "open", "--limit", "500", "--json", "number,title,body,comments,labels"}, &open); err != nil {
		return held, err
	}
	for _, is := range open {
		targets[is.Number] = &commandTarget{number: is.Number, title: is.Title, body: is.Body, comments: is.Comments, labeled: issueLabeled(is.Labels, wf.label), parked: issueLabeled(is.Labels, "needs-human")}
		order = append(order, is.Number)
	}
	for _, n := range order {
		if steerIssue(repo, base, login, wf, targets[n]) {
			held[n] = true
		}
	}
	prs, err := listPRs(repo)
	if err != nil {
		return held, err
	}
	for _, p := range prs {
		if num := agentIssue(p); num > 0 {
			var prior []prComment
			if t, ok := targets[num]; ok {
				prior = t.comments
			}
			steerPR(repo, base, login, wf, p, num, prior)
		}
	}
	return held, nil
}

func steerIssue(repo, base, login string, wf workflow, t *commandTarget) bool {
	c := newestCommand(t.comments, login)
	if c == nil || consumed(repo, c, login) {
		return false
	}
	verb, _ := parseCommand(c.Body)
	n := strconv.Itoa(t.number)
	var err error
	switch {
	case t.parked && (verb == "answer" || verb == "retry"):
		stepf(base, t.number, "command "+verb)
		_, err = gh(repo, "issue", "edit", n, "--add-label", wf.label, "--remove-label", "needs-human")
		if err == nil {
			openCard(repo, issue{Number: t.number, Comments: t.comments}, login).note(commandEvent[verb])
		}
	case verb == "start" && startApplies(*t):
		stepf(base, t.number, "command start")
		_, err = gh(repo, "issue", "edit", n, "--add-label", wf.label, "--remove-label", "needs-human")
		if err == nil {
			is := issue{Number: t.number, Title: t.title, Body: t.body, Comments: t.comments}
			openCard(repo, is, login).approve(issueDigest(is))
		}
	case verb == "close":
		stepf(base, t.number, "command close")
		_, err = gh(repo, "issue", "close", n, "--reason", "completed")
	case verb == "stop":
		stepf(base, t.number, "command stop")
		_, err = gh(repo, "issue", "edit", n, "--remove-label", wf.label, "--remove-label", "needs-human")
	default:
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s #%d command %s: %v\n", base, t.number, verb, err)
		return false
	}
	ackCommand(repo, c)
	return verb == "stop" || verb == "close"
}

func steerPR(repo, base, login string, wf workflow, p pr, num int, prior []prComment) {
	c := newestCommand(p.Comments, login)
	if c == nil || consumed(repo, c, login) {
		return
	}
	verb, _ := parseCommand(c.Body)
	n := strconv.Itoa(num)
	var err error
	switch verb {
	case "retry":
		if !weParked(p.Comments, login) || parkedByUs(p.Comments, login) {
			return
		}
		stepf(base, num, "command retry")
		if _, err = gh(repo, "issue", "edit", n, "--add-label", wf.label, "--remove-label", "needs-human"); err == nil {
			openCard(repo, issue{Number: num, Comments: prior}, login).note(commandEvent["retry"])
		}
	case "close":
		stepf(base, num, "command close")
		if _, err = ghOwner(repo, "pr", "close", strconv.Itoa(p.Number), "--delete-branch"); err == nil {
			spec := commentSpec{kind: "park", role: "engine", number: num, sentence: "The owner closed the pull request.", footer: retryFooter()}
			err = park(parkPlace{repo: repo, base: base, login: login, label: wf.label, target: n, issue: num, prior: prior}, "owner closed the pull request", spec)
		}
	case "stop":
		stepf(base, num, "command stop")
		_, err = gh(repo, "issue", "edit", n, "--remove-label", wf.label, "--remove-label", "needs-human")
	default:
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s #%d command %s: %v\n", base, num, verb, err)
		return
	}
	ackCommand(repo, c)
}

func parkedByUs(comments []prComment, login string) bool {
	parked := false
	for _, c := range comments {
		switch {
		case c.Author.Login == login && strings.HasPrefix(c.Body, parkMarker):
			parked = true
		case !engineWrote(c, login) && trusted(c):
			if verb, _ := parseCommand(c.Body); verb == "retry" {
				parked = false
			}
		}
	}
	return parked
}

func parseCommand(body string) (string, string) {
	words := strings.Fields(body)
	if len(words) == 0 {
		return "", ""
	}
	switch verb := strings.ToLower(words[0]); verb {
	case "/answer":
		text := strings.TrimSpace(strings.TrimSpace(body)[len(words[0]):])
		if text == "" {
			return "", ""
		}
		return "answer", text
	case "/retry", "/close", "/stop", "/start":
		return verb[1:], ""
	}
	return "", ""
}

var sharedLogin = true

func engineWrote(c prComment, login string) bool {
	return c.Author.Login == login && (!sharedLogin || engineComment(c.Body))
}

func newestCommand(comments []prComment, login string) *prComment {
	var found *prComment
	for i := range comments {
		if engineWrote(comments[i], login) || !trusted(comments[i]) {
			continue
		}
		if verb, _ := parseCommand(comments[i].Body); verb != "" {
			found = &comments[i]
		}
	}
	return found
}

func weParked(comments []prComment, login string) bool {
	for _, c := range comments {
		if c.Author.Login == login && strings.HasPrefix(c.Body, parkMarker) {
			return true
		}
	}
	return false
}

type reaction struct {
	Content string `json:"content"`
	User    author `json:"user"`
}

func ackCommand(repo string, c *prComment) {
	id := commentID(c.URL)
	if id == "" {
		return
	}
	if _, err := gh(repo, "api", "-X", "POST", "repos/{owner}/{repo}/issues/comments/"+id+"/reactions", "-f", "content=+1"); err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: reaction: %v\n", err)
	}
}

func consumed(repo string, c *prComment, login string) bool {
	id := commentID(c.URL)
	if id == "" {
		return false
	}
	var rs []reaction
	if err := ghJSON(repo, []string{"api", "repos/{owner}/{repo}/issues/comments/" + id + "/reactions"}, &rs); err != nil {
		return false
	}
	for _, r := range rs {
		if strings.TrimSuffix(r.User.Login, "[bot]") == login && r.Content == "+1" {
			return true
		}
	}
	return false
}
