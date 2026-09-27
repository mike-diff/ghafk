package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func runIssue(home, repo, base, login string, wf workflow, contract string, is issue) error {
	n := strconv.Itoa(is.Number)
	startWorking(repo, n)
	defer stopWorking(repo, n)
	c := openCard(repo, is, login)
	at := parkPlace{repo: repo, base: base, login: login, label: wf.label, target: n, issue: is.Number, prior: is.Comments, card: c}
	worker, werr := workerForLabels(wf, is.Labels)
	if werr != nil {
		spec := commentSpec{kind: "park", role: "worker", number: is.Number, sentence: werr.Error(), footer: retryFooter()}
		return park(at, werr.Error(), spec)
	}
	c.begin("Implement", worker.Label)

	stepf(base, is.Number, "branch")
	def, err := defaultBranch(repo)
	if err != nil {
		return err
	}

	stepf(base, is.Number, "fetch")
	if _, err := run(repo, "git", "fetch", "origin"); err != nil {
		return err
	}

	branch := "agent/" + n
	work := workDir(home, repo, n)
	if err := clearStale(repo, work, branch); err != nil {
		return err
	}

	stepf(base, is.Number, "worktree")
	if err := addWorktree(repo, work, "-b", branch, "origin/"+def); err != nil {
		return err
	}
	defer worktreeRemove(repo, work)
	before, err := run(work, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	stepf(base, is.Number, "worker")
	prompt := joinParts(workerPrompt(wf), wf.body, contract, "# Issue #"+n+": "+is.Title, strings.TrimSpace(is.Body))
	answer, tokens, err := runWorker(base, is.Number, work, worker, prompt, wf.timeout)
	if err != nil {
		return runFailed(repo, base, is, wf.label, "worker", err, wf.timeout, login, c)
	}
	status, changed, err := workChanges(work, before)
	if err != nil {
		return parkOnError(at, "worker", "ghafk could not read the worker's changes.", err)
	}
	if !changed {
		stepf(base, is.Number, "no changes")
		c.finish("failed", tokens)
		spec := commentSpec{kind: "park", role: "worker", number: is.Number, sentence: "The worker made no changes.", footer: retryFooter()}
		return park(at, "the worker made no changes", spec)
	}

	subject := commitSubject(contract, is.Title)
	stepf(base, is.Number, "commit")
	if err := commitChanges(work, status, subject, "Closes #"+n); err != nil {
		return parkOnError(at, "commit", "The commit failed.", err)
	}

	stepf(base, is.Number, "push")
	if _, err := run(work, "git", "push", "-u", "origin", branch); err != nil {
		return parkOnError(at, "push", "The push was rejected.", err)
	}

	stepf(base, is.Number, "pr")
	stat, err := run(work, "git", "diff", "--stat", "origin/"+def+"...HEAD")
	if err != nil {
		return parkOnError(at, "pr", "ghafk could not list the changes.", err)
	}
	prURL, err := ghOwner(work, "pr", "create", "--title", subject, "--head", branch, "--body", renderPRBody(is.Number, summaryLines(answer), stat, prState{}))
	if err != nil {
		return parkOnError(at, "pr", "Opening the pull request failed.", err)
	}
	c.setPR(prURL)
	c.finish("done", tokens)
	c.queue()
	return nil
}

func runFailed(repo, base string, is issue, label, role string, runErr error, limit time.Duration, login string, c *card) error {
	sentence, body := runFailure(role, runErr, limit)
	spec := commentSpec{kind: "park", role: role, number: is.Number, sentence: sentence, body: body, footer: retryFooter()}
	return park(parkPlace{repo: repo, base: base, login: login, label: label, target: strconv.Itoa(is.Number), issue: is.Number, prior: is.Comments, card: c}, sentence, spec)
}

func runWorker(base string, n int, work string, worker harness.Role, prompt string, timeout time.Duration) (string, int, error) {
	var out bytes.Buffer
	if err := runShell("worker", work, worker.Command, prompt, timeout, &out, os.Stderr); err != nil {
		return "", 0, err
	}
	answer, tokens := reportRoleUsage(base, n, worker, out.String())
	if answer != "" {
		fmt.Println(answer)
	}
	return answer, tokens, nil
}

func workChanges(work, since string) (string, bool, error) {
	status, err := run(work, "git", "status", "--porcelain")
	if err != nil {
		return "", false, err
	}
	head, err := run(work, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	return status, workerChanged(status, head, since), nil
}

func commitChanges(work, status string, messages ...string) error {
	if status == "" {
		return nil
	}
	if _, err := run(work, "git", "add", "-A"); err != nil {
		return err
	}
	args := []string{"commit"}
	for _, m := range messages {
		args = append(args, "-m", m)
	}
	_, err := run(work, "git", args...)
	return err
}
