package harness

import (
	"fmt"
	"strconv"
	"strings"
)

// UsageTokens returns the first number in a "usage:" line, or 0.
func UsageTokens(usage string) int {
	for _, field := range strings.Fields(strings.ReplaceAll(usage, ",", "")) {
		if n, err := strconv.Atoi(field); err == nil {
			return n
		}
	}
	return 0
}

type Usage struct {
	Tokens  int
	Cost    float64
	HasCost bool
	Known   bool
}

func (u Usage) String() string {
	if u.HasCost {
		return fmt.Sprintf("%d tokens $%.4f", u.Tokens, u.Cost)
	}
	return fmt.Sprintf("%d tokens", u.Tokens)
}

// SplitUsage separates a trailing "usage:" line from a worker's output.
func SplitUsage(output string) (answer, usage string) {
	lines := strings.Split(strings.TrimRight(output, " \t\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "usage:") {
			return strings.TrimSpace(strings.Join(lines[:i], "\n")), strings.TrimSpace(strings.TrimPrefix(line, "usage:"))
		}
		break
	}
	return strings.TrimSpace(output), ""
}
