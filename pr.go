package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const checkTimeout = 15 * time.Minute

func checksPassedAt(p pr, tip string) bool {
	st, ok := parsePRState(p.Body)
	return ok && st.Checks == "pass" && st.ChecksSha == tip
}

func verdictPosted(p pr, login, kind, sha string) bool {
	if st, ok := parsePRState(p.Body); ok && st.Verdict == kind && st.VerdictSha == sha {
		return true
	}
	head := verdictHead(kind, sha)
	for _, c := range append(append([]prComment(nil), p.Comments...), p.Reviews...) {
		if c.Author.Login == login && strings.HasPrefix(c.Body, head) {
			return true
		}
	}
	return false
}

type prRun struct {
	home, repo, base, login string
	wf                      workflow
	pick                    pr
	is                      issue
	n                       int
	prNum, issueNum, branch string
	card                    *card
	at                      parkPlace
	def, work, lease        string
	baseSha, before, after  string
	st                      prState
	hasState, green         bool
	tail, reason            string
	checkErr                error
	checkOut                string
}

func workPR(home, repo, base, login string, wf workflow, prs []pr) (bool, error) {
	pick, is, n, err := pickPR(repo, login, wf, prs)
	if err != nil || n == 0 {
		return false, err
	}
	r := &prRun{home: home, repo: repo, base: base, login: login, wf: wf, pick: pick, is: is, n: n,
		prNum: strconv.Itoa(pick.Number), issueNum: strconv.Itoa(n), branch: pick.HeadRefName}
	startWorking(repo, r.issueNum)
	defer stopWorking(repo, r.issueNum)
	r.card = openCard(repo, is, login)
	if prURL, err := gh(repo, "pr", "view", r.prNum, "--json", "url", "--jq", ".url"); err == nil {
		r.card.setPR(prURL)
	}
	r.at = parkPlace{repo: repo, base: base, login: login, label: wf.label, target: r.prNum, issue: n, prior: pick.Comments, card: r.card}
	if wf.checks == "" {
		spec := commentSpec{kind: "park", role: "checks", number: n, sentence: "No checks are configured for this repository.", footer: retryFooter()}
		return true, park(r.at, "no checks configured", spec)
	}
	stepf(base, n, "checks")
	r.card.begin("Checks", "")
	if err := r.checkout(); err != nil {
		return true, err
	}
	defer worktreeRemove(repo, r.work)
	if err := r.check(); err != nil {
		return true, err
	}
	if r.green {
		if held, err := r.holdProtected(); held || err != nil {
			return true, err
		}
		if parked, err := r.judge(); parked || err != nil {
			return true, err
		}
	}
	if r.green {
		return true, r.merge()
	}
	if !r.repairSpent() {
		if done, err := r.repair(); done || err != nil {
			return true, err
		}
	}
	return true, r.parkFailed()
}

func pickPR(repo, login string, wf workflow, prs []pr) (pr, issue, int, error) {
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
	for _, p := range prs {
		num := agentIssue(p)
		if num == 0 {
			continue
		}
		var view issueView
		if err := ghJSON(repo, []string{"issue", "view", strconv.Itoa(num), "--json", "number,author,title,body,comments,state,labels"}, &view); err != nil {
			return pr{}, issue{}, 0, err
		}
		if parkedByUs(withIssueRetries(p.Comments, view.Comments, login), login) {
			continue
		}
		if issueOpen(view.State) && issueLabeled(view.Labels, wf.label) {
			return p, view.issue, num, nil
		}
	}
	return pr{}, issue{}, 0, nil
}

func (r *prRun) checkout() error {
	var err error
	if r.def, err = defaultBranch(r.repo); err != nil {
		return err
	}
	if _, err := run(r.repo, "git", "fetch", "origin"); err != nil {
		return err
	}
	r.work = workDir(r.home, r.repo, r.issueNum)
	if err := clearStale(r.repo, r.work, r.branch); err != nil {
		return err
	}
	if _, err := run(r.repo, "git", "fetch", "origin", "+refs/heads/"+r.branch+":refs/heads/"+r.branch); err != nil {
		return err
	}
	if err := addWorktree(r.repo, r.work, r.branch); err != nil {
		return err
	}
	if r.baseSha, err = run(r.work, "git", "rev-parse", "origin/"+r.def); err != nil {
		return err
	}
	if r.before, err = run(r.work, "git", "rev-parse", "HEAD"); err != nil {
		return err
	}
	r.lease = "--force-with-lease=refs/heads/" + r.branch + ":" + r.before
	r.st, r.hasState = parsePRState(r.pick.Body)
	r.after = r.before
	r.tail = "The branch conflicts with " + r.def + "."
	r.reason = "checks failed"
	return nil
}

func (r *prRun) check() error {
	if _, err := run(r.work, "git", "rebase", "origin/"+r.def); err != nil {
		_, err := run(r.work, "git", "rebase", "--abort")
		return err
	}
	var err error
	if r.after, err = run(r.work, "git", "rev-parse", "HEAD"); err != nil {
		return err
	}
	if checksPassedAt(r.pick, r.after) {
		r.green = true
		r.st.Checks, r.st.ChecksSha = "pass", r.after
		r.card.finish("done", 0)
	} else {
		var buf bytes.Buffer
		r.checkErr = runShell("checks", r.work, r.wf.checks, "", checkTimeout, &buf, &buf)
		if r.checkErr == nil {
			r.green = true
			r.st.Checks, r.st.ChecksSha = "pass", r.after
			r.card.finish("done", 0)
		} else {
			r.checkOut = buf.String()
			lines := strings.Split(r.checkOut, "\n")
			if len(lines) > 200 {
				lines = lines[len(lines)-200:]
			}
			r.tail = strings.Join(lines, "\n")
			r.st.Checks, r.st.ChecksSha = "fail", r.after
			fmt.Fprintf(os.Stderr, "%s #%d checks failed:\n%s\n", r.base, r.n, r.tail)
		}
	}
	return r.putBody("")
}

func (r *prRun) putBody(summary string) error {
	return putPRBody(r.repo, r.prNum, r.work, r.def, r.n, summary, r.st, r.pick.Body)
}

func (r *prRun) judge() (bool, error) {
	stepf(r.base, r.n, "judge")
	if verdictPosted(r.pick, r.login, "approve", r.after) {
		return false, nil
	}
	r.card.begin("Judge", r.wf.judge.Label)
	verdict, tokens, err := runJudge(r.base, r.n, r.work, r.wf.judge, r.wf.body, judgeContract(r.is, r.login), r.def, r.wf.timeout)
	if err != nil {
		return true, parkOnError(r.at, "judge", "The judge failed to run.", err)
	}
	kind := ""
	switch {
	case strings.HasPrefix(verdict, "approve:"):
		kind = "approve"
	case strings.HasPrefix(verdict, "reject:"):
		kind = "reject"
	default:
		r.st.Repairs = 0
		if err := r.putBody(""); err != nil {
			return false, err
		}
		spec := commentSpec{kind: "park", role: "judge", number: r.n, sentence: "The judge returned no verdict.", body: detailsBlock("Model output", firstLines(verdict, 40)), footer: retryFooter()}
		return true, park(r.at, "judge returned no verdict", spec)
	}
	items := parseItems(verdict)
	r.card.setItems(items)
	if err := postVerdict(r.repo, r.prNum, r.pick.Author.Login, r.login, kind, renderVerdict(kind, r.after, items)); err != nil {
		return false, err
	}
	r.st.Verdict, r.st.VerdictSha = kind, r.after
	if err := r.putBody(""); err != nil {
		return false, err
	}
	if kind == "approve" {
		r.card.finish("done", tokens)
		return false, nil
	}
	r.card.record("judge rejected", "")
	r.card.finish("failed", tokens)
	r.green = false
	r.reason = "rejected"
	r.tail = verdict
	return false, nil
}

func (r *prRun) merge() error {
	stepf(r.base, r.n, "merge")
	r.card.begin("Merge", "")
	head, err := run(r.work, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != r.after {
		spec := commentSpec{kind: "park", role: "merge", number: r.n, sentence: "The branch changed after the checks ran.", body: "ghafk checked `" + shortSha(r.after) + "`, but the worktree is now at `" + shortSha(head) + "`. ghafk merges only the commit that it checked.", footer: retryFooter()}
		return park(r.at, "branch changed after checks", spec)
	}
	if _, err := run(r.work, "git", "push", r.lease, "origin", r.after+":refs/heads/"+r.branch); err != nil {
		return err
	}
	worktreeRemove(r.repo, r.work)
	if merged, err := mergeOrPark(r.at, r.prNum, commitSubject(contractText(r.is.Comments, r.login), r.is.Title), r.after); !merged || err != nil {
		return err
	}
	r.card.merged()
	if _, err := run(r.repo, "git", "fetch", "origin"); err != nil {
		return err
	}
	tip, err := run(r.repo, "git", "rev-parse", "origin/"+r.def)
	if err != nil {
		return err
	}
	if err := reconcile(r.home, r.repo, r.base, r.login, r.wf, r.def, r.baseSha, tip, r.n); err != nil {
		fmt.Fprintf(os.Stderr, "%s: reconcile: %v\n", r.base, err)
	}
	_, err = gh(r.repo, "issue", "edit", r.issueNum, "--remove-label", r.wf.label)
	return err
}

func (r *prRun) repairSpent() bool {
	if r.hasState {
		return r.st.Repairs > 0
	}
	return repairedSincePark(r.pick.Comments, r.login)
}

func (r *prRun) repair() (bool, error) {
	stepf(r.base, r.n, "repair")
	worker, werr := workerForLabels(r.wf, r.is.Labels)
	if werr != nil {
		spec := commentSpec{kind: "park", role: "worker", number: r.n, sentence: werr.Error(), footer: retryFooter()}
		return true, park(r.at, werr.Error(), spec)
	}
	r.card.repair(worker.Label)
	prompt := joinParts(workerPrompt(r.wf), r.wf.body, contractText(r.is.Comments, r.login),
		"# Repair: PR #"+r.prNum+" for issue #"+r.issueNum+" failed checks",
		r.tail,
		"# Issue #"+r.issueNum+": "+r.is.Title,
		strings.TrimSpace(r.is.Body))
	answer, tokens, err := runWorker(r.base, r.n, r.work, worker, prompt, r.wf.timeout)
	if err != nil {
		sentence, body := runFailure("worker", err, r.wf.timeout)
		spec := commentSpec{kind: "park", role: "worker", number: r.n, sentence: sentence, body: body, footer: retryFooter()}
		return true, park(r.at, sentence, spec)
	}
	status, changed, err := workChanges(r.work, r.after)
	if err != nil {
		return true, err
	}
	if !changed {
		spec := commentSpec{kind: "repair", role: "worker", number: r.n, sentence: "The repair worker made no changes."}
		return false, postComment(r.repo, r.prNum, spec, r.pick.Comments, r.login)
	}
	if err := commitChanges(r.work, status, "fix: repair checks for #"+r.issueNum); err != nil {
		return true, err
	}
	sha, err := run(r.work, "git", "rev-parse", "HEAD")
	if err != nil {
		return true, err
	}
	if _, err := run(r.work, "git", "push", r.lease, "origin", r.branch); err != nil {
		return true, err
	}
	r.card.finish("done", tokens)
	r.card.queue()
	r.st = prState{Repairs: r.st.Repairs + 1}
	if err := r.putBody(summaryLines(answer)); err != nil {
		return true, err
	}
	spec := commentSpec{kind: "repair", role: "worker", number: r.n, sentence: "Repair 1 pushed " + sha + "."}
	return true, postComment(r.repo, r.prNum, spec, r.pick.Comments, r.login)
}

func (r *prRun) parkFailed() error {
	r.st.Repairs = 0
	if err := r.putBody(""); err != nil {
		return err
	}
	spec := commentSpec{kind: "park", role: "checks", number: r.n, sentence: "The checks failed twice.", footer: retryFooter()}
	switch {
	case r.reason == "rejected":
		spec.role = "judge"
		spec.sentence = "The judge rejected the diff twice."
		spec.body = strings.TrimSpace(strings.TrimPrefix(r.tail, "reject:"))
	case r.checkErr != nil:
		spec.body = failureEvidence(r.wf.checks, r.checkErr, r.checkOut, checkTimeout)
	default:
		spec.body = r.tail
	}
	return park(r.at, r.reason+" twice", spec)
}

func repairedSincePark(comments []prComment, login string) bool {
	repaired := false
	for _, c := range comments {
		switch {
		case c.Author.Login == login && strings.HasPrefix(c.Body, repairMarker):
			repaired = true
		case c.Author.Login == login && strings.HasPrefix(c.Body, parkMarker):
			repaired = false
		}
	}
	return repaired
}

func mergeOrPark(at parkPlace, prNum, subject, head string) (bool, error) {
	_, err := ghOwner(at.repo, "pr", "merge", prNum, "--squash", "--subject", subject, "--body", "", "--delete-branch", "--match-head-commit", head)
	if err == nil {
		return true, nil
	}
	return false, parkOnError(at, "merge", "GitHub refused the merge.", err)
}

func withIssueRetries(prComments, issueComments []prComment, login string) []prComment {
	all := append([]prComment(nil), prComments...)
	for _, c := range issueComments {
		if verb, _ := parseCommand(c.Body); verb == "retry" && trusted(c) && !engineWrote(c, login) {
			all = append(all, c)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt < all[j].CreatedAt })
	return all
}

func protectedPaths(files []string) []string {
	var held []string
	for _, f := range files {
		if strings.HasPrefix(f, ".github/") || strings.HasPrefix(f, ".ghafk/") {
			held = append(held, f)
		}
	}
	return held
}

func (r *prRun) holdProtected() (bool, error) {
	out, err := run(r.work, "git", "diff", "--name-only", "origin/"+r.def+"...HEAD")
	if err != nil {
		return false, err
	}
	held := protectedPaths(strings.Split(out, "\n"))
	if len(held) == 0 {
		return false, nil
	}
	spec := commentSpec{kind: "park", role: "merge", number: r.n, sentence: "The change touches paths ghafk never merges on its own.", body: fenced(strings.Join(held, "\n")) + "\n\nReview the change and merge it yourself, or `/close` it.", footer: retryFooter()}
	return true, park(r.at, "protected paths", spec)
}
