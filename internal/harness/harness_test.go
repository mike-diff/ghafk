package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const claudeFixture = `{"type":"result","result":"OK","total_cost_usd":0.0240588,"usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":13868,"cache_creation_input_tokens":11325}}`

const codexFixture = `{"type":"thread.started","thread_id":"01a0da65-fe25-72a2-9a78-6c827cacc211"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OK"}}
{"type":"turn.completed","usage":{"input_tokens":16185,"cached_input_tokens":12160,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0}}`

const piFixture = `{"type":"tool_call","name":"sh","args":{"command":"ls"}}
{"type":"agent_end","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"text","text":"OK"}],"usage":{"input":16455,"output":3,"cacheRead":3968,"cacheWrite":0,"totalTokens":20426,"cost":{"total":0.02408188}}}]}`

func TestParseClaudeJSONFixture(t *testing.T) {
	reply, u, ok := parseClaudeJSON(claudeFixture)
	if !ok {
		t.Fatal("the claude fixture did not decode")
	}
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
	if u.Tokens != 25199 {
		t.Fatalf("tokens = %d, want 25199", u.Tokens)
	}
	if !u.HasCost || u.Cost != 0.0240588 {
		t.Fatalf("cost = %v known = %v, want 0.0240588 known", u.Cost, u.HasCost)
	}
	if got := u.String(); got != "25199 tokens $0.0241" {
		t.Fatalf("usage line = %q, want today's format", got)
	}
}

func TestParseCodexJSONFixture(t *testing.T) {
	reply, u, ok := parseCodexJSON(codexFixture)
	if !ok {
		t.Fatal("the codex fixture did not decode")
	}
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
	if u.Tokens != 16190 {
		t.Fatalf("tokens = %d, want 16190", u.Tokens)
	}
	if u.HasCost {
		t.Fatalf("cost = %v, want unknown", u.Cost)
	}
	if got := u.String(); got != "16190 tokens" {
		t.Fatalf("usage line = %q, want no cost", got)
	}
}

func TestParsePIJSONFixture(t *testing.T) {
	reply, u, ok := parsePIJSON(piFixture)
	if !ok {
		t.Fatal("the pi fixture did not decode")
	}
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
	if u.Tokens != 20426 {
		t.Fatalf("tokens = %d, want 20426", u.Tokens)
	}
	if !u.HasCost || u.Cost != 0.02408188 {
		t.Fatalf("cost = %v known = %v, want 0.02408188 known", u.Cost, u.HasCost)
	}
	if got := u.String(); got != "20426 tokens $0.0241" {
		t.Fatalf("usage line = %q, want today's format", got)
	}
}

func TestParseTextFixture(t *testing.T) {
	reply, u := ParseOutput("text", "OK\n")
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
	if u.Known {
		t.Fatalf("usage = %+v, want unknown", u)
	}
}

func TestJSONParserFallsBackToRawStdout(t *testing.T) {
	reply, u := ParseOutput("claude-json", "not json at all\n")
	if reply != "not json at all" {
		t.Fatalf("reply = %q, want the raw stdout", reply)
	}
	if u.Known {
		t.Fatalf("usage = %+v, want none", u)
	}
}

func TestHarnessFileOverridesAndAdds(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "harnesses")
	if err := os.WriteFile(file, []byte("pi: text pi-lite --model {model}\nllm: codex-json llm exec --model {model}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	henv, err := LoadEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if henv.Profiles["pi"].Parser != "text" || henv.Profiles["pi"].Command != "pi-lite --model {model}" {
		t.Fatalf("the pi override was not applied: %#v", henv.Profiles["pi"])
	}
	if henv.Profiles["llm"].Parser != "codex-json" || henv.Profiles["llm"].Command != "llm exec --model {model}" {
		t.Fatalf("the new profile was not added: %#v", henv.Profiles["llm"])
	}
	if henv.Profiles["claude"].Parser != "claude-json" {
		t.Fatalf("a built-in must survive: %#v", henv.Profiles["claude"])
	}
	if err := os.WriteFile(file, []byte("pi text pi-lite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadEnv(dir)
	if err == nil || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), ":1:") {
		t.Fatalf("err = %v, want it to name the file and line", err)
	}
}

func TestHarnessFileAllowList(t *testing.T) {
	file := filepath.Join(t.TempDir(), "harnesses")
	text := "claude.allow: opus sonnet\nclaude: claude-json claude -p --model {model}\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	henv, err := applyHarnessFile(Env{Profiles: Builtins()}, file)
	if err != nil {
		t.Fatal(err)
	}
	p := henv.Profiles["claude"]
	if strings.Join(p.Allow, ",") != "opus,sonnet" {
		t.Fatalf("allow = %v, want [opus sonnet]", p.Allow)
	}
	if p.Parser != "claude-json" || p.Command != "claude -p --model {model}" {
		t.Fatalf("a later profile line must still set the command: %#v", p)
	}
}

func TestParsePIJSONSystemMessageWithStringContent(t *testing.T) {
	stdout := `{"type":"agent_start"}
{"type":"agent_end","messages":[{"role":"system","content":"You are pi."},{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"x"},{"type":"toolCall","name":"bash"}],"usage":{"totalTokens":100,"cost":{"total":0.5}}},{"role":"toolResult","content":[{"type":"text","text":"a.txt"}]},{"role":"assistant","content":[{"type":"text","text":"OK"}],"usage":{"totalTokens":20,"cost":{"total":0.25}}}]}`
	reply, u := ParseOutput("pi-json", stdout)
	if reply != "OK" || u.Tokens != 120 || u.Cost != 0.75 {
		t.Fatalf("got reply %q tokens %d cost %v, want OK 120 0.75", reply, u.Tokens, u.Cost)
	}
}

func TestSeshPromptIsTheValueOfP(t *testing.T) {
	r, err := ResolveRole("sesh model-a", Builtins())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.Command, `-p "$(cat)"`) {
		t.Fatalf("sesh reads the word after -p as the prompt, so -p must take the prompt last: %q", r.Command)
	}
}
func TestUsageTokensTakesTheFirstNumber(t *testing.T) {
	if got := UsageTokens("230,672 tokens $0.1600"); got != 230672 {
		t.Fatalf("usageTokens = %d, want 230672", got)
	}
	if got := UsageTokens(""); got != 0 {
		t.Fatalf("usageTokens with no usage = %d, want 0", got)
	}
}

func TestSplitUsageStripsTrailingUsageLine(t *testing.T) {
	answer, usage := SplitUsage("contract: x\n\nusage: 12 tokens $0.0300 \r\n\n")
	if answer != "contract: x" {
		t.Fatalf("answer = %q", answer)
	}
	if usage != "12 tokens $0.0300" {
		t.Fatalf("usage = %q", usage)
	}
}

func TestSplitUsageWithoutUsageLineKeepsOutput(t *testing.T) {
	answer, usage := SplitUsage("  plain answer \n")
	if answer != "plain answer" {
		t.Fatalf("answer = %q", answer)
	}
	if usage != "" {
		t.Fatalf("usage = %q", usage)
	}
}

func TestLoadEnvWithoutFilesUsesTheBuiltins(t *testing.T) {
	henv, err := LoadEnv(t.TempDir())
	if err != nil {
		t.Fatalf("missing harness and config files must not be an error: %v", err)
	}
	if len(henv.Profiles) != len(Builtins()) || henv.Default != "" {
		t.Fatalf("got %d profiles and default %q, want the built-ins and no default", len(henv.Profiles), henv.Default)
	}
}

func TestModelMayNotLookLikeAFlag(t *testing.T) {
	for _, bad := range []string{"--dangerously-skip-permissions", "-x", ".hidden"} {
		if ValidModel.MatchString(bad) {
			t.Errorf("model %q is accepted, so a model: label could pass a flag to the harness", bad)
		}
	}
	if !ValidModel.MatchString("provider/model-1.5:max") {
		t.Error("an ordinary model id was rejected")
	}
}

func TestHarnessFileAllowsComments(t *testing.T) {
	dir := t.TempDir()
	text := "# my agents\nllm: text llm --model {model}\n"
	if err := os.WriteFile(filepath.Join(dir, "harnesses"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	henv, err := LoadEnv(dir)
	if err != nil {
		t.Fatalf("a comment line broke the harness file: %v", err)
	}
	if henv.Profiles["llm"].Command != "llm --model {model}" {
		t.Fatalf("profile after a comment was lost: %#v", henv.Profiles["llm"])
	}
}
