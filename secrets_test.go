package main

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

func dummyGitHubToken() string { return "ghp" + "_" + strings.Repeat("A", 36) }

func resetSecretRegistry(t *testing.T) {
	t.Helper()
	secretRegistry.Lock()
	old := secretRegistry.forms
	secretRegistry.forms = nil
	secretRegistry.Unlock()
	t.Cleanup(func() {
		secretRegistry.Lock()
		secretRegistry.forms = old
		secretRegistry.Unlock()
	})
}

func TestAKnownValueIsFoundInItsEncodedForms(t *testing.T) {
	resetSecretRegistry(t)
	value := "run-credential-" + strings.Repeat("q7", 12)
	registerSecret(value)
	for _, shown := range []string{
		value,
		hex.EncodeToString([]byte(value)),
		strings.ToUpper(hex.EncodeToString([]byte(value))),
		base64.StdEncoding.EncodeToString([]byte(value)),
		base64.StdEncoding.EncodeToString([]byte("x" + value)),
		base64.StdEncoding.EncodeToString([]byte("xy" + value + "z")),
		base64.URLEncoding.EncodeToString([]byte("key=" + value)),
	} {
		if got := redactSecrets("before " + shown + " after"); strings.Contains(got, shown) {
			t.Errorf("an encoded form survived redaction: %s", got)
		}
	}
	if got := redactSecrets("short text"); got != "short text" {
		t.Fatalf("text without a secret changed: %q", got)
	}
}

func TestPatternsFindKeysButNotBase64Runs(t *testing.T) {
	resetSecretRegistry(t)
	token := dummyGitHubToken()
	if found := findSecrets("TOKEN=" + token); len(found) == 0 {
		t.Fatal("a GitHub token after an equals sign must be found")
	}
	google := "AIza" + strings.Repeat("B", 35)
	if found := findSecrets("sha512-" + strings.Repeat("x", 20) + google + "=="); len(found) != 0 {
		t.Fatalf("a match inside a base64 run must not count: %v", found)
	}
	if found := findSecrets("key: " + google); len(found) == 0 {
		t.Fatal("a standalone Google key must be found")
	}
}

func TestEverythingPostedToGitHubIsRedacted(t *testing.T) {
	resetSecretRegistry(t)
	calls := fakeGH(t, nil)
	token := dummyGitHubToken()
	registerSecret("posted-credential-" + strings.Repeat("z", 10))
	if _, err := gh(".", "issue", "comment", "5", "--body", "leak "+token+" and posted-credential-"+strings.Repeat("z", 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := ghOwner(".", "pr", "create", "--title", "t "+token, "--body", "b"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		joined := c.String()
		if strings.Contains(joined, token) || strings.Contains(joined, "posted-credential") {
			t.Fatalf("a secret reached gh: %s", joined)
		}
		if !strings.Contains(joined, secretPlaceholder) {
			t.Fatalf("the secret was not replaced: %s", joined)
		}
	}
}

func secretRepo(t *testing.T) (string, string) {
	t.Helper()
	dir, base := gitRepo(t)
	return dir, base
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "git", "commit", "-q", "-m", message); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsANewSecretWithItsFileAndLine(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	commitFile(t, dir, "config/app.env", "NAME=x\nTOKEN="+dummyGitHubToken()+"\n", "feat: add config")
	hits, err := scanChange(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].where != `"config/app.env" line 2` {
		t.Fatalf("want one hit at config/app.env line 2, got %v", hits)
	}
	if strings.Contains(secretParkBody(hits, true), dummyGitHubToken()) {
		t.Fatal("the park body quoted the secret")
	}
}

func TestScanChecksFileNamesAndCommitMessages(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	commitFile(t, dir, "notes."+dummyGitHubToken()+".txt", "x\n", "feat: add notes")
	if hits, _ := scanChange(dir, base, nil); len(hits) != 1 || hits[0].where != "a file name" {
		t.Fatalf("a secret in a file name must be found once, as a file name: %v", hits)
	}
	dir, base = secretRepo(t)
	commitFile(t, dir, "notes.txt", "x\n", "feat: add notes with "+dummyGitHubToken())
	hits, _ := scanChange(dir, base, nil)
	if len(hits) == 0 || hits[0].where != "the commit message" {
		t.Fatalf("a secret in the commit message must be found: %v", hits)
	}
}

func TestAnAlreadyCommittedPatternValueIsNotNew(t *testing.T) {
	resetSecretRegistry(t)
	dir, _ := secretRepo(t)
	commitFile(t, dir, "old.env", "TOKEN="+dummyGitHubToken()+"\n", "chore: old secret")
	base, _ := run(dir, "git", "rev-parse", "HEAD")
	commitFile(t, dir, "copy.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: copy")
	if hits, _ := scanChange(dir, base, nil); len(hits) != 0 {
		t.Fatalf("a value that the base already holds must not count as new: %v", hits)
	}
	registerSecret(dummyGitHubToken())
	if hits, _ := scanChange(dir, base, nil); len(hits) == 0 {
		t.Fatal("a value that ghafk passed into the run always counts, even if the base holds it")
	}
}

func TestTheAllowlistCoversPatternsButNotKnownValues(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	commitFile(t, dir, "testdata/keys.txt", dummyGitHubToken()+"\n", "test: fixture")
	if hits, _ := scanChange(dir, base, []string{"testdata/"}); len(hits) != 0 {
		t.Fatalf("an allowed path must not hold the push: %v", hits)
	}
	registerSecret(dummyGitHubToken())
	if hits, _ := scanChange(dir, base, []string{"testdata/"}); len(hits) == 0 {
		t.Fatal("the allowlist must not cover a value that ghafk passed into the run")
	}
}

func TestSecretsAllowRefusesPathsOutsideTheRepository(t *testing.T) {
	for _, bad := range []string{"/etc", "../other"} {
		if _, err := parseWorkflow("---\nworker: sh\nsecrets-allow: "+bad+"\n---\n", harness.Env{Profiles: harness.Builtins()}); err == nil {
			t.Errorf("secrets-allow %q must be refused", bad)
		}
	}
}

func TestAnUnscannedPushFromAWorktreeIsRefused(t *testing.T) {
	repo := gitClone(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := addWorktree(repo, work, "-b", "agent/7", "origin/main"); err != nil {
		t.Fatal(err)
	}
	defer worktreeRemove(repo, work)
	commitFile(t, work, "a.txt", "a\n", "feat: a")
	if _, err := run(work, "git", "push", "origin", "agent/7"); err == nil || !strings.Contains(err.Error(), "not checked for secrets") {
		t.Fatalf("an unscanned push must be refused: %v", err)
	}
	markScanned(work)
	if _, err := run(work, "git", "push", "origin", "agent/7"); err != nil {
		t.Fatalf("a scanned push must go through: %v", err)
	}
	commitFile(t, work, "b.txt", "b\n", "feat: b")
	if _, err := run(work, "git", "push", "origin", "agent/7"); err == nil {
		t.Fatal("a commit made after the scan must be refused")
	}
}

func originHas(t *testing.T, repo, branch string) string {
	t.Helper()
	origin, err := run(repo, "git", "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := run(origin, "git", "log", "-p", "--all", "--format=%B")
	return out
}

func TestAWorkerThatRemovesASecretWhenAskedIsPushed(t *testing.T) {
	resetSecretRegistry(t)
	repo := gitClone(t)
	token := dummyGitHubToken()
	worker := harnessScript(t, `in=$(cat)
case "$in" in
*"ghafk did not push your change"*) rm -f leak.env; echo ok > fixed.txt ;;
*) echo "TOKEN=`+token+`" > leak.env; echo app > app.txt ;;
esac
`)
	calls := runIssueWith(t, repo, worker)
	if called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a worker that removed the secret when asked must not park: %v", calls)
	}
	pushed := originHas(t, repo, "agent/5")
	if strings.Contains(pushed, token) {
		t.Fatal("the secret reached the remote")
	}
	if !strings.Contains(pushed, "fixed.txt") || !strings.Contains(pushed, "app.txt") {
		t.Fatalf("the repaired change was not pushed:\n%s", pushed)
	}
}

func TestAWorkerThatKeepsASecretParksWithoutPushing(t *testing.T) {
	resetSecretRegistry(t)
	repo := gitClone(t)
	token := dummyGitHubToken()
	calls := runIssueWith(t, repo, `echo "TOKEN=`+token+`" > leak.env`)
	if !called(calls, "issue edit 5 --add-label needs-human --remove-label agent") {
		t.Fatalf("a worker that kept the secret must park: %v", calls)
	}
	requireSecretPark(t, calls)
	if strings.Contains(originHas(t, repo, "agent/5"), token) {
		t.Fatal("the secret reached the remote")
	}
	for _, c := range calls {
		if strings.Contains(c.String(), token) {
			t.Fatalf("the park comment quoted the secret: %s", c)
		}
	}
}

func TestTheMergePushHoldsABranchWithASecret(t *testing.T) {
	resetSecretRegistry(t)
	calls := fakeGH(t, nil)
	repo := gitClone(t)
	work := filepath.Join(t.TempDir(), "work")
	if _, err := run(repo, "git", "worktree", "add", "-q", "-b", "agent/5", work); err != nil {
		t.Fatal(err)
	}
	commitFile(t, work, "leak.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: leak")
	checked, _ := run(work, "git", "rev-parse", "HEAD")
	if err := mergeRun(t, repo, work, checked).merge(); err != nil {
		t.Fatal(err)
	}
	if called(*calls, "pr merge") {
		t.Fatalf("a branch holding a secret was merged: %v", *calls)
	}
	if !called(*calls, "issue edit 5 --add-label needs-human") {
		t.Fatalf("the branch was not handed to the owner: %v", *calls)
	}
}

func repairRun(t *testing.T, script string) (*prRun, string) {
	t.Helper()
	repo := gitClone(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := addWorktree(repo, work, "-b", "agent/5", "origin/main"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { worktreeRemove(repo, work) })
	commitFile(t, work, "app.txt", "app\n", "feat: app")
	markScanned(work)
	if _, err := run(work, "git", "push", "-q", "origin", "agent/5"); err != nil {
		t.Fatal(err)
	}
	after, _ := run(work, "git", "rev-parse", "HEAD")
	r := mergeRun(t, repo, work, after)
	r.issueNum, r.home = "5", t.TempDir()
	r.wf = workflow{label: "agent", worker: harness.Role{Command: harnessScript(t, script)}, timeout: time.Minute}
	return r, after
}

func TestARepairThatKeepsASecretIsNotPushed(t *testing.T) {
	needSandbox(t)
	resetSecretRegistry(t)
	calls := fakeGH(t, nil)
	r, after := repairRun(t, `cat >/dev/null; echo "TOKEN=`+dummyGitHubToken()+`" > leak.env`+"\n")
	if _, err := r.repair(); err != nil {
		t.Fatal(err)
	}
	if tip, _ := run(r.repo, "git", "ls-remote", "origin", "agent/5"); !strings.HasPrefix(tip, after) {
		t.Fatalf("the repair with a secret reached the remote: %s", tip)
	}
	if !called(*calls, "issue edit 5 --add-label needs-human") {
		t.Fatalf("the repair was not parked: %v", *calls)
	}
	requireSecretPark(t, *calls)
}

func requireSecretPark(t *testing.T, calls []ghCall) {
	t.Helper()
	for _, c := range calls {
		text := c.String()
		if strings.Contains(text, "ghafk asked the worker to remove the values") && strings.Contains(text, "GitHub token") {
			return
		}
	}
	t.Fatalf("the park must come from the secret check after the worker was asked to remove the value: %v", calls)
}

func TestARepairThatRemovesASecretWhenAskedIsPushed(t *testing.T) {
	needSandbox(t)
	resetSecretRegistry(t)
	fakeGH(t, nil)
	r, after := repairRun(t, `in=$(cat)
case "$in" in
*"ghafk did not push your change"*) rm -f leak.env; echo fixed > fix.txt ;;
*) echo "TOKEN=`+dummyGitHubToken()+`" > leak.env ;;
esac
`)
	if _, err := r.repair(); err != nil {
		t.Fatal(err)
	}
	tip, _ := run(r.repo, "git", "ls-remote", "origin", "agent/5")
	if strings.HasPrefix(tip, after) {
		t.Fatal("the cleaned repair was not pushed")
	}
	if strings.Contains(originHas(t, r.repo, "agent/5"), dummyGitHubToken()) {
		t.Fatal("the secret reached the remote")
	}
}

func TestRunKeysAndLoginTokensAreRegistered(t *testing.T) {
	resetSecretRegistry(t)
	key := "run-api-key-" + strings.Repeat("k", 16)
	t.Setenv("ANTHROPIC_API_KEY", key)
	sandboxEnv(sandboxSpec{profile: "claude", path: "/usr/bin"}, "/h", "http://127.0.0.1:1")
	if got := redactSecrets("key " + key); strings.Contains(got, key) {
		t.Fatalf("an API key passed into the run was not registered: %s", got)
	}
	login := filepath.Join(t.TempDir(), "auth.json")
	refresh := "refresh-token-" + strings.Repeat("r", 20)
	if err := os.WriteFile(login, []byte(`{"tokens":{"refresh_token":"`+refresh+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registerSecretFile(login)
	if got := redactSecrets("x " + refresh); strings.Contains(got, refresh) {
		t.Fatalf("a login token was not registered: %s", got)
	}
}

func TestContentThatLooksLikeADiffHeaderIsStillScanned(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	known := "registered-credential-" + strings.Repeat("c", 10)
	registerSecret(known)
	commitFile(t, dir, "production.env", "++ b/testdata/fixture.txt\n++ "+known+"\nTOKEN="+dummyGitHubToken()+"\n", "feat: env")
	hits, err := scanChange(dir, base, []string{"testdata/"})
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for _, h := range hits {
		where = append(where, h.where)
	}
	joined := strings.Join(where, ",")
	if !strings.Contains(joined, `"production.env" line 2`) || !strings.Contains(joined, `"production.env" line 3`) {
		t.Fatalf("a content line shaped like a diff header hid a secret or faked an allowed path: %v", hits)
	}
}

func TestGitDisplaySettingsDoNotBlindTheScan(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	for _, kv := range [][]string{{"color.ui", "always"}, {"color.diff", "always"}, {"diff.noprefix", "true"}, {"diff.mnemonicPrefix", "true"}, {"grep.lineNumber", "true"}, {"grep.column", "true"}, {"core.quotePath", "true"}} {
		if _, err := run(dir, "git", "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, dir, "config/app.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: config")
	hits, err := scanChange(dir, base, nil)
	if err != nil || len(hits) != 1 || hits[0].where != `"config/app.env" line 1` {
		t.Fatalf("git display settings changed what the scan sees: %v %v", hits, err)
	}
	commitFile(t, dir, "old.env", "TOKEN="+dummyGitHubToken()+"\n", "chore: old")
	base2, _ := run(dir, "git", "rev-parse", "HEAD")
	commitFile(t, dir, "copy.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: copy")
	if hits, err := scanChange(dir, base2, nil); err != nil || len(hits) != 0 {
		t.Fatalf("the base-tree lookup broke under grep settings: %v %v", hits, err)
	}
}

func TestProviderConfigKeysAreRegistered(t *testing.T) {
	resetSecretRegistry(t)
	dir := t.TempDir()
	jsonKey := "custom-provider-key-" + strings.Repeat("j", 12)
	tomlKey := "toml-provider-key-" + strings.Repeat("t", 12)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"mine":{"baseUrl":"https://models.example.test/v1","apiKey":"`+jsonKey+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "config.toml"), []byte("model = \"some-model-name\"\napi_key = \""+tomlKey+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registerConfigSecrets(dir)
	for _, key := range []string{jsonKey, tomlKey} {
		if got := redactSecrets("x " + key); strings.Contains(got, key) {
			t.Errorf("a provider config key was not registered: %s", key)
		}
	}
	for _, plain := range []string{"https://models.example.test/v1", "some-model-name"} {
		if got := redactSecrets("x " + plain); !strings.Contains(got, plain) {
			t.Errorf("a non-secret config value was registered: %s", plain)
		}
	}
}

func TestBuildingASandboxRegistersItsProviderConfig(t *testing.T) {
	resetSecretRegistry(t)
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "run-anthropic-key-"+strings.Repeat("a", 10))
	key := "pi-provider-key-" + strings.Repeat("p", 12)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "models.json"), []byte(`{"providers":{"mine":{"apiKey":"`+key+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: "sh", Profile: "pi"}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "sh", timeout: time.Minute, role: &role, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := redactSecrets("x " + key); strings.Contains(got, key) {
		t.Fatal("a key in a copied provider config was not registered")
	}
}

func TestAFileNameStartingWithANewlineIsStillScanned(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	commitFile(t, dir, "\nleak.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: add config")
	hits, err := scanChange(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("a secret in a file whose name starts with a newline must be found: %v", hits)
	}
}

func TestASymlinkedProviderConfigRegistersItsKey(t *testing.T) {
	resetSecretRegistry(t)
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "run-anthropic-key-"+strings.Repeat("a", 10))
	key := "linked-provider-key-" + strings.Repeat("l", 12)
	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotfiles, "models.json"), []byte(`{"providers":{"mine":{"apiKey":"`+key+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "models.json"), filepath.Join(home, ".pi", "agent", "models.json")); err != nil {
		t.Fatal(err)
	}
	role := harness.Role{Command: "sh", Profile: "pi"}
	spec, err := buildSandbox(sandboxOpts{name: "worker", dir: t.TempDir(), command: "sh", timeout: time.Minute, role: &role, home: home})
	defer removeTree(spec.runDir)
	defer removeTree(spec.sockDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := redactSecrets("x " + key); strings.Contains(got, key) {
		t.Fatal("a key in a symlinked provider config was copied in but not registered")
	}
}

func TestQuotedConfigKeysAreRegistered(t *testing.T) {
	resetSecretRegistry(t)
	dir := t.TempDir()
	jsoncKey := "jsonc-provider-key-" + strings.Repeat("c", 12)
	tomlKey := "quoted-toml-key-" + strings.Repeat("q", 12)
	jsonc := "{\n  // my providers\n  \"apiKey\": \"" + jsoncKey + "\",\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte(jsonc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("\"api_key\" = \""+tomlKey+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registerConfigSecrets(dir)
	for _, key := range []string{jsoncKey, tomlKey} {
		if got := redactSecrets("x " + key); strings.Contains(got, key) {
			t.Errorf("a quoted config key was not registered: %s", key)
		}
	}
}

func TestCopyBackRegistersOnlyTokenFields(t *testing.T) {
	resetSecretRegistry(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := []byte(`{"tokens":{"access_token":"old"}}`)
	real := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(real, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := sandboxSpec{runHome: t.TempDir()}
	run := filepath.Join(spec.runHome, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(run), 0o755); err != nil {
		t.Fatal(err)
	}
	token := "refreshed-access-token-" + strings.Repeat("r", 12)
	planted := ".defaultBranchRef.name-" + strings.Repeat("n", 12)
	if err := os.WriteFile(run, []byte(`{"tokens":{"access_token":"`+token+`"},"note":"`+planted+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.logins = []loginSeed{{real: real, run: run, runResolved: evalPath(run), orig: orig}}
	syncBack(t, spec)
	if got := redactSecrets("x " + token); strings.Contains(got, token) {
		t.Fatal("a refreshed token field was not registered")
	}
	if got := redactSecrets("x " + planted); !strings.Contains(got, planted) {
		t.Fatal("a non-token string from the run's login copy was registered")
	}
}

func TestAHitFileNameCannotBreakOutOfThePostedList(t *testing.T) {
	resetSecretRegistry(t)
	dir, base := secretRepo(t)
	commitFile(t, dir, "x`\n@someone Closes #1.env", "TOKEN="+dummyGitHubToken()+"\n", "feat: add config")
	hits, err := scanChange(dir, base, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("want one hit, got %v %v", hits, err)
	}
	body := secretParkBody(hits, true)
	if strings.Contains(body, "\n@someone") {
		t.Fatalf("a file name wrote a raw line into the park comment:\n%s", body)
	}
	if !strings.HasPrefix(body, "```") {
		t.Fatalf("the hit list must be fenced:\n%s", body)
	}
}

func TestConfigReferencesToVariablesAreNotSecrets(t *testing.T) {
	resetSecretRegistry(t)
	dir := t.TempDir()
	real := "real-provider-key-" + strings.Repeat("k", 12)
	files := map[string]string{
		"config.toml":                  "env_key = \"AZURE_OPENAI_API_KEY\"\napi_key = \"${OPENAI_KEY_NAME}\"\n",
		"models.json":                  `{"providers":{"a":{"apiKey":"OPENROUTER_API_KEY"},"b":{"apiKey":"{env:MY_PROVIDER_TOKEN}"},"c":{"apiKey":"` + real + `"}}}`,
		"plugin/tool.js":               "const token: process.env.GITHUB_TOKEN_FOR_TOOLS\n",
		"README.md":                    "api_key = docs-example-placeholder-value\n",
		"node_modules/x/settings.json": `{"apiKey":"vendored-key-` + strings.Repeat("v", 12) + `"}`,
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registerConfigSecrets(dir)
	for _, name := range []string{"AZURE_OPENAI_API_KEY", "${OPENAI_KEY_NAME}", "OPENROUTER_API_KEY", "{env:MY_PROVIDER_TOKEN}", "process.env.GITHUB_TOKEN_FOR_TOOLS", "vendored-key-" + strings.Repeat("v", 12), "docs-example-placeholder-value"} {
		if got := redactSecrets("x " + name); !strings.Contains(got, name) {
			t.Errorf("a variable reference or non-config value was registered as a secret: %s", name)
		}
	}
	if got := redactSecrets("x " + real); strings.Contains(got, real) {
		t.Fatal("a literal provider key must still be registered")
	}
}

func TestRedactionLeavesGhFlagsAndPathsAlone(t *testing.T) {
	resetSecretRegistry(t)
	planted := ".defaultBranchRef.name-" + strings.Repeat("n", 8)
	registerSecret(planted)
	args := redactArgs([]string{"repo", "view", "--jq", planted, "--body", "x " + planted, "-f", "body=y " + planted, "repos/{owner}/{repo}/" + planted})
	if args[3] != planted || args[len(args)-1] != "repos/{owner}/{repo}/"+planted {
		t.Fatalf("a registered value rewrote a gh flag or path: %v", args)
	}
	if strings.Contains(args[5], planted) || strings.Contains(args[7], planted) {
		t.Fatalf("content arguments were not redacted: %v", args)
	}
}

func TestTheOwnersGhLoginIsAKnownSecret(t *testing.T) {
	resetSecretRegistry(t)
	bin := t.TempDir()
	token := "gho-login-token-" + strings.Repeat("o", 12)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n[ \"$1 $2\" = \"auth token\" ] && echo "+token+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	registerOwnerToken(t.TempDir())
	if got := redactSecrets("x " + token); strings.Contains(got, token) {
		t.Fatal("the owner's gh login token was not registered")
	}
}
