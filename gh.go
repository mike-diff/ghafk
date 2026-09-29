package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func commentID(url string) string {
	_, id, ok := strings.Cut(url, "#issuecomment-")
	if !ok || id == "" {
		return ""
	}
	return id
}

func startWorking(repo, issueNum string) {
	if _, err := gh(repo, "label", "create", workingLabel, "--color", "1D76DB", "--description", "a ghafk run is active", "--force"); err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: %v\n", err)
		return
	}
	if _, err := gh(repo, "issue", "edit", issueNum, "--add-label", workingLabel); err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: %v\n", err)
	}
}

func stopWorking(repo, issueNum string) {
	if _, err := gh(repo, "issue", "edit", issueNum, "--remove-label", workingLabel); err != nil {
		fmt.Fprintf(os.Stderr, "ghafk: %v\n", err)
	}
}

const commentLimit = 100

type author struct {
	Login string `json:"login"`
}

type prComment struct {
	Body        string `json:"body"`
	Author      author `json:"author"`
	URL         string `json:"url"`
	Association string `json:"authorAssociation"`
	CreatedAt   string `json:"createdAt"`
}

type pr struct {
	Number      int         `json:"number"`
	HeadRefName string      `json:"headRefName"`
	CrossRepo   bool        `json:"isCrossRepository"`
	Author      author      `json:"author"`
	Body        string      `json:"body"`
	Comments    []prComment `json:"comments"`
	Reviews     []prComment `json:"reviews"`
}

func listPRs(repo string) ([]pr, error) {
	var all []pr
	if err := ghJSON(repo, []string{"pr", "list", "--state", "open", "--author", ownerLogin, "--limit", "500", "--json", "number,headRefName,isCrossRepository,author,body,comments,reviews"}, &all); err != nil {
		return nil, err
	}
	var own []pr
	for _, p := range all {
		if agentIssue(p) > 0 && !p.CrossRepo && p.Author.Login == ownerLogin {
			own = append(own, p)
		}
	}
	return own, nil
}

type label struct {
	Name string `json:"name"`
}

type issueView struct {
	issue
	State string `json:"state"`
}

func issueLabeled(labels []label, name string) bool {
	for _, l := range labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

func ensureNeedsHumanLabel(repo string) error {
	_, err := gh(repo, "label", "create", "needs-human", "--color", "D93F0B", "--description", "ghafk gave up; a human decides", "--force")
	return err
}

func needHuman(repo, issueNum, label string) error {
	if err := ensureNeedsHumanLabel(repo); err != nil {
		return err
	}
	_, err := gh(repo, "issue", "edit", issueNum, "--add-label", "needs-human", "--remove-label", label)
	return err
}

func issueOpen(state string) bool {
	return strings.EqualFold(state, "open")
}

type assignee struct {
	Login string `json:"login"`
}

type issue struct {
	Number    int         `json:"number"`
	Author    author      `json:"author"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Assignees []assignee  `json:"assignees"`
	Comments  []prComment `json:"comments"`
	Labels    []label     `json:"labels"`
}

var (
	engineToken string
	ownerLogin  string
)

const tokenRefresh = 50 * time.Minute

var (
	engineRemint   func() (string, error)
	engineMintedAt time.Time
)

func ghEnv() []string {
	env := runEnv()
	if engineToken != "" && engineRemint != nil && time.Since(engineMintedAt) > tokenRefresh {
		if token, err := engineRemint(); err == nil {
			engineToken, engineMintedAt = token, time.Now()
			registerSecret(token)
		} else {
			fmt.Fprintf(os.Stderr, "ghafk: app token refresh failed: %v\n", err)
		}
	}
	if engineToken != "" {
		return append(env, "GH_TOKEN="+engineToken)
	}
	return withOwnerToken(env)
}

var runGH = func(repo string, env []string, args ...string) (string, error) {
	return runWithEnv(repo, "gh", env, args...)
}

func gh(repo string, args ...string) (string, error) {
	out, err := runGH(repo, ghEnv(), redactArgs(args)...)
	return out, err
}

func ghOwner(repo string, args ...string) (string, error) {
	out, err := runGH(repo, withOwnerToken(runEnv()), redactArgs(args)...)
	return out, err
}

func ghJSON(repo string, args []string, v any) error {
	out, err := gh(repo, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("gh %s: %v", args[0], err)
	}
	return nil
}

func listIssues(repo, label string) ([]issue, error) {
	var issues []issue
	err := ghJSON(repo, []string{"issue", "list", "--label", label, "--state", "open", "--limit", "500", "--json", "number,author,title,body,assignees,comments,labels"}, &issues)
	return issues, err
}

func agentIssue(p pr) int {
	num, err := strconv.Atoi(strings.TrimPrefix(p.HeadRefName, "agent/"))
	if err != nil || num <= 0 || !strings.HasPrefix(p.HeadRefName, "agent/") {
		return 0
	}
	return num
}

func defaultBranch(repo string) (string, error) {
	return gh(repo, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")
}
