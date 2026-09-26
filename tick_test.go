package main

import "testing"

func TestPickIssueSkipsIssuesHeldThisTick(t *testing.T) {
	issues := []issue{{Number: 61}, {Number: 64}, {Number: 70}}
	if got := pickIssue(issues, map[int]bool{64: true}, map[int]bool{61: true}); got != 2 {
		t.Fatalf("picked index %d, want 2: #61 was stopped this tick and #64 has an open PR", got)
	}
	if got := pickIssue(issues, nil, nil); got != 0 {
		t.Fatalf("picked index %d, want the lowest number", got)
	}
}
