package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

func groomIssue(home, repo, base, login string, wf workflow, is issue) error {
	n := strconv.Itoa(is.Number)
	startWorking(repo, n)
	defer stopWorking(repo, n)
	c := openCard(repo, is, login)
	at := parkPlace{repo: repo, base: base, login: login, label: wf.label, target: n, issue: is.Number, prior: is.Comments, card: c}
	if c.st.Contract != "" {
		c.supersede(wf.groomer.Label)
	} else {
		c.begin("Groom", wf.groomer.Label)
	}

	stepf(base, is.Number, "groom")

	def, err := defaultBranch(repo)
	if err != nil {
		return err
	}
	if _, err := run(repo, "git", "fetch", "origin"); err != nil {
		return err
	}

	branch := "groom/" + n
	work := runWork(home, repo, n)
	if err := clearStale(repo, work, branch); err != nil {
		return err
	}
	if err := addWorktree(repo, work, "-b", branch, "origin/"+def); err != nil {
		return err
	}
	defer discardWork(repo, work, branch)

	var out bytes.Buffer
	if err := runSandboxed(sandboxOpts{name: "groomer", dir: work, command: wf.groomer.Command, stdin: groomPrompt(wf, is, login), timeout: wf.timeout, role: &wf.groomer, egress: wf.egress, repo: repo, home: home}, &out, os.Stderr); err != nil {
		stepf(base, is.Number, "needs-human")
		return runFailed(repo, base, is, wf.label, "groomer", err, wf.timeout, login, c)
	}

	answer, tokens := reportRoleUsage(base, is.Number, wf.groomer, redactSecrets(out.String()))
	answer = answerFrom(answer, "contract:", "question:")
	switch {
	case strings.HasPrefix(answer, "contract:") && strings.TrimSpace(strings.TrimPrefix(answer, "contract:")) != "":
		stepf(base, is.Number, "contract")
		c.st.Contract = strings.TrimSpace(strings.TrimPrefix(answer, "contract:"))
		c.st.Groomed = issueDigest(is)
		c.finish("done", tokens)
		c.queue()
		if c.err != nil {
			return parkOnError(at, "groomer", "The status card could not save the contract.", c.err)
		}
		return nil
	case strings.HasPrefix(answer, "question:"):
		stepf(base, is.Number, "question")
		question, options := parseQuestion(strings.TrimPrefix(answer, "question:"))
		spec := commentSpec{kind: "question", role: "groomer", number: is.Number, sentence: "The groomer needs your answer.", body: questionBody(question, options), footer: questionFooter()}
		return park(at, question, spec)
	default:
		spec := commentSpec{kind: "park", role: "groomer", number: is.Number, sentence: "The groomer returned no contract.", body: detailsBlock("Model output", firstLines(answer, 40)), footer: retryFooter()}
		return park(at, "groomer returned no contract", spec)
	}
}

func groomPrompt(wf workflow, is issue, login string) string {
	parts := []string{rolePrompt("groom"), wf.body, "# Issue #" + strconv.Itoa(is.Number) + ": " + is.Title, strings.TrimSpace(is.Body)}
	for _, cm := range is.Comments {
		switch {
		case engineWrote(cm, login) || (cm.Author.Login == login && strings.HasPrefix(cm.Body, "<details><summary>Superseded")):
			parts = append(parts, "## Earlier engine comment", strings.TrimSpace(cm.Body))
		case trusted(cm):
			parts = append(parts, "## Comment by "+cm.Author.Login, strings.TrimSpace(cm.Body))
		}
	}
	return joinParts(parts...)
}
