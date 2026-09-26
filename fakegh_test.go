package main

import (
	"strings"
	"testing"
)

type ghCall struct {
	args []string
	env  []string
}

func (c ghCall) String() string { return strings.Join(c.args, " ") }

func fakeGH(t *testing.T, respond func(cmd string) (string, error)) *[]ghCall {
	t.Helper()
	calls := &[]ghCall{}
	old := runGH
	runGH = func(repo string, env []string, args ...string) (string, error) {
		c := ghCall{args: append([]string(nil), args...), env: env}
		*calls = append(*calls, c)
		if respond == nil {
			return "", nil
		}
		return respond(c.String())
	}
	t.Cleanup(func() { runGH = old })
	return calls
}

func called(calls []ghCall, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c.String(), prefix) {
			return true
		}
	}
	return false
}

func TestFakeGHRecordsEngineCalls(t *testing.T) {
	calls := fakeGH(t, func(cmd string) (string, error) {
		if cmd == "api user --jq .login" {
			return "octo", nil
		}
		return "", nil
	})
	out, _ := gh(".", "api", "user", "--jq", ".login")
	if out != "octo" || !called(*calls, "api user") {
		t.Fatalf("gh went around the seam: out %q, calls %v", out, *calls)
	}
}
