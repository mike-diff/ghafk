package main

import "github.com/mike-diff/ghafk/internal/harness"

func reportRoleUsage(base string, n int, r harness.Role, output string) (string, int) {
	if r.Parser == "" {
		return reportUsage(base, n, output)
	}
	reply, u := harness.ParseOutput(r.Parser, output)
	if u.Known {
		stepf(base, n, "usage "+u.String())
	}
	return reply, u.Tokens
}

func reportUsage(base string, n int, output string) (string, int) {
	answer, usage := harness.SplitUsage(output)
	tokens := harness.UsageTokens(usage)
	if usage != "" {
		stepf(base, n, "usage "+usage)
	}
	return answer, tokens
}
