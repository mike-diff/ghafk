package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mike-diff/ghafk/internal/ghapp"
	"github.com/mike-diff/ghafk/internal/harness"
)

func tick() (err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unlock, err := lockTick(home)
	if errors.Is(err, errTickRunning) {
		fmt.Println("ghafk: another tick is running; this one stops")
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	if _, ok := installedEngine(); !ok {
		os.Chmod(filepath.Join(home, ".ghafk"), 0o700)
	}
	if stop, logErr := startTickLog(home); logErr == nil {
		defer stop()
	} else {
		fmt.Fprintf(os.Stderr, "ghafk: tick log: %v\n", logErr)
	}
	st := engineStatus{Harnesses: []string{}}
	defer func() {
		st.Time = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			st.Error = err.Error()
		}
		if werr := writeEngineStatus(home, st); werr != nil {
			fmt.Fprintf(os.Stderr, "ghafk: status file: %v\n", werr)
		}
	}()
	if err := loadEngineEnv(); err != nil {
		return err
	}
	henv, err := loadHarnessEnv()
	if err != nil {
		return err
	}
	if err := sandboxReady(); err != nil {
		return err
	}
	sweepStaleRuns(home, time.Now())
	st.Harnesses = installedHarnesses(henv)
	cfg, err := loadMachineSettings()
	if err != nil {
		return err
	}
	progressColumns = cfg.progress
	owner, expires, err := ownerAccount(home)
	if err != nil {
		return err
	}
	registerOwnerToken(home)
	if w := tokenWarning(owner, expires, time.Now()); w != "" {
		fmt.Println(w)
	}
	ownerLogin = owner
	st.Owner = owner
	if !expires.IsZero() {
		st.TokenExpires = expires.UTC().Format(time.RFC3339)
	}
	mint := appMinter()
	targets, err := engineRepos(home, cfg.skip, cfg.repos, true)
	if err != nil {
		return err
	}
	for _, t := range targets {
		var login string
		engineToken, login = engineIdentity(owner, func() (ghapp.Identity, error) { return mint(t.name) })
		registerSecret(engineToken)
		engineMintedAt = time.Now()
		engineRemint = func() (string, error) {
			id, err := mint(t.name)
			return id.Token, err
		}
		sharedLogin = login == owner
		st.Account = login
		state := "ready"
		if err := workRepo(home, t.path, login, henv); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(t.path), err)
			state = "error: " + err.Error()
		}
		st.Repos = append(st.Repos, repoStatus{Name: t.name, State: state})
	}
	return nil
}

var errTickRunning = errors.New("another tick is running")

func lockTick(home string) (func(), error) {
	path := filepath.Join(home, ".ghafk", "tick.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errTickRunning
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

func readRepos(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			repos = append(repos, line)
		}
	}
	return repos, nil
}

func workRepo(home, repo, login string, henv harness.Env) error {
	base := filepath.Base(repo)
	canWrite = writeChecker(repo)
	if _, err := run(repo, "git", "fetch", "-q", "origin"); err != nil {
		return err
	}
	wf, err := loadWorkflow(repo, henv)
	if err != nil {
		return err
	}
	held, err := steer(repo, base, login, wf)
	if err != nil {
		return err
	}
	prs, err := listPRs(repo)
	if err != nil {
		return err
	}
	handled, err := workPR(home, repo, base, login, wf, prs)
	if err != nil || handled {
		return err
	}
	issues, err := listIssues(repo, wf.label)
	if err != nil {
		return err
	}
	inFlight := map[int]bool{}
	for _, p := range prs {
		if num := agentIssue(p); num > 0 {
			inFlight[num] = true
		}
	}
	pick := pickIssue(issues, inFlight, held)
	if pick < 0 {
		fmt.Printf("%s: no open %s issues\n", base, wf.label)
		return nil
	}
	is := issues[pick]
	if tooManyComments(is.Comments) {
		return parkTooManyComments(repo, base, login, wf.label, is)
	}
	st := openCard(repo, is, login).st
	if needsApproval(is, st) {
		return parkForApproval(repo, base, login, wf.label, is)
	}
	if contractText(is.Comments, login) != "" && !staleAfterContract(is.Comments, login) && !editedSinceGroom(is, st) {
		err = runIssue(home, repo, base, login, wf, contractText(is.Comments, login), is)
	} else {
		err = groomIssue(home, repo, base, login, wf, is)
	}
	return countFailure(home, repo, base, login, wf.label, is, err)
}

const stuckLimit = 5

func stuckFile(home, repo string, n int) string {
	return filepath.Join(home, ".ghafk", "stuck", repoSlug(repo)+"-"+strconv.Itoa(n))
}

func countFailure(home, repo, base, login, label string, is issue, runErr error) error {
	path := stuckFile(home, repo, is.Number)
	if runErr == nil {
		os.Remove(path)
		return nil
	}
	err := fmt.Errorf("#%d: %w", is.Number, runErr)
	data, _ := os.ReadFile(path)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if count++; count < stuckLimit {
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			os.WriteFile(path, []byte(strconv.Itoa(count)), 0o600)
		}
		return err
	}
	os.Remove(path)
	spec := commentSpec{kind: "park", role: "engine", number: is.Number, sentence: fmt.Sprintf("This issue failed on %d ticks in a row.", stuckLimit), body: fenced(homeRelative(runErr.Error())), footer: retryFooter()}
	if perr := park(parkPlace{repo: repo, base: base, login: login, label: label, target: strconv.Itoa(is.Number), issue: is.Number, prior: is.Comments}, "failed on every tick", spec); perr != nil {
		return errors.Join(err, perr)
	}
	return err
}

func tooManyComments(lists ...[]prComment) bool {
	for _, l := range lists {
		if len(l) >= commentLimit {
			return true
		}
	}
	return false
}

func parkTooManyComments(repo, base, login, label string, is issue) error {
	stepf(base, is.Number, "too many comments")
	spec := commentSpec{kind: "park", role: "engine", number: is.Number, sentence: fmt.Sprintf("This issue or its pull request has %d or more comments.", commentLimit), body: fmt.Sprintf("GitHub gives ghafk only the first %d comments, so ghafk cannot see the newest state or commands. Open a new issue that links to this one.", commentLimit)}
	return park(parkPlace{repo: repo, base: base, login: login, label: label, target: strconv.Itoa(is.Number), issue: is.Number, prior: is.Comments}, "too many comments", spec)
}

func parkForApproval(repo, base, login, label string, is issue) error {
	stepf(base, is.Number, "needs approval")
	spec := commentSpec{kind: "park", role: "engine", number: is.Number, sentence: "Someone without write access opened this issue.", body: "A maintainer must read the title and description, then comment `/start`. ghafk works only that exact text. Agents do not see HTML comments, link definitions or invisible characters. If the text changes later, ghafk stops and needs a new `/start`.", footer: approvalFooter()}
	return park(parkPlace{repo: repo, base: base, login: login, label: label, target: strconv.Itoa(is.Number), issue: is.Number, prior: is.Comments}, "approval needed", spec)
}

func stepf(base string, n int, name string) {
	fmt.Printf("%s #%d %s\n", base, n, name)
}

func joinParts(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n") + "\n"
}

func pickIssue(issues []issue, inFlight, held map[int]bool) int {
	pick := -1
	for i, is := range issues {
		if len(is.Assignees) == 0 && !inFlight[is.Number] && !held[is.Number] && (pick < 0 || is.Number < issues[pick].Number) {
			pick = i
		}
	}
	return pick
}
