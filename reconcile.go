package main

import (
	"bytes"
	"os"
	"sort"
	"strconv"
	"strings"
)

func reconcile(home, repo, base, login string, wf workflow, def, before, after string, landedIssue int) error {
	out, err := run(repo, "git", "diff", "--name-only", before, after)
	if err != nil {
		return err
	}
	landed := strings.Fields(out)
	var issues []issue
	for _, l := range []string{wf.label, "needs-human"} {
		var list []issue
		if err := ghJSON(repo, []string{"issue", "list", "--state", "open", "--label", l, "--limit", "500", "--json", "number,author,title,body,comments,labels"}, &list); err != nil {
			return err
		}
		issues = append(issues, list...)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	prs, err := listPRs(repo)
	if err != nil {
		return err
	}
	taken := map[int]bool{}
	for _, p := range prs {
		if num := agentIssue(p); num > 0 {
			taken[num] = true
		}
	}
	matches := 0
	for i, is := range issues {
		if i > 0 && is.Number == issues[i-1].Number || taken[is.Number] || is.Number == landedIssue {
			continue
		}
		contract := contractText(is.Comments, login)
		files := contractFiles(contract)
		if !overlaps(files, landed) {
			continue
		}
		if err := reconcileIssue(home, repo, base, login, wf, contract, before, after, is, files); err != nil {
			return err
		}
		if matches++; matches == 5 {
			break
		}
	}
	return nil
}

func reconcileIssue(home, repo, base, login string, wf workflow, contract, before, after string, is issue, files []string) error {
	n := strconv.Itoa(is.Number)
	startWorking(repo, n)
	defer stopWorking(repo, n)
	stepf(base, is.Number, "reconcile")
	branch := "reconcile/" + n
	work := workDir(home, repo, n)
	if err := clearStale(repo, work, branch); err != nil {
		return err
	}
	if err := addWorktree(repo, work, "-b", branch, after); err != nil {
		return err
	}
	defer discardWork(repo, work, branch)
	diff, err := run(repo, "git", append([]string{"diff", before, after, "--"}, files...)...)
	if err != nil {
		return err
	}
	prompt := joinParts(rolePrompt("reconcile"), wf.body, contract, "# Landed\n"+capDiff(diff))
	var out bytes.Buffer
	if err := runShell("reconciler", work, wf.reconciler.Command, prompt, wf.timeout, &out, os.Stderr); err != nil {
		return err
	}
	answer, _ := reportRoleUsage(base, is.Number, wf.reconciler, out.String())
	answer = answerFrom(answer, "valid:", "stale:", "done:")
	verdict := "needs-human"
	for _, v := range []string{"valid", "stale", "done"} {
		if strings.HasPrefix(answer, v+":") {
			verdict = v
		}
	}
	stepf(base, is.Number, verdict)
	rest := strings.TrimSpace(strings.TrimPrefix(answer, verdict+":"))
	spec := commentSpec{kind: "park", role: "reconciler", number: is.Number, sentence: "The reconciler returned no verdict.", body: detailsBlock("Model output", firstLines(rest, 40)), footer: retryFooter()}
	switch verdict {
	case "valid":
		spec = commentSpec{kind: "reconcile-valid", role: "reconciler", number: is.Number, sentence: "The reconciler marked the contract valid at " + after + ".", body: rest}
	case "stale":
		spec = commentSpec{kind: "reconcile-stale", role: "reconciler", number: is.Number, sentence: "The reconciler marked the contract stale at " + after + ".", body: rest}
	case "done":
		spec = commentSpec{kind: "park", role: "reconciler", number: is.Number, sentence: "The reconciler says the landed code already satisfies this contract.", body: rest, footer: closeFooter()}
	}
	if verdict == "valid" || verdict == "stale" {
		return postComment(repo, n, spec, is.Comments, login)
	}
	return park(parkPlace{repo: repo, base: base, login: login, label: wf.label, target: n, issue: is.Number, prior: is.Comments}, spec.sentence, spec)
}
