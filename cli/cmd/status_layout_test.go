package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/status"
)

// A value that runs past the terminal continues under its own column. Printing
// the continuation at column zero puts prose where a label belongs, which is what
// the dashboard's stacked labels avoid.
func TestEveryWrappedLineLandsInItsOwnColumn(t *testing.T) {
	long := strings.Repeat("a long sentence that has to wrap more than once. ", 6)
	s := &status.SystemStatus{StoreProblems: []status.StoreProblem{{Scope: "project", Error: long}}}

	problems := captureStdout(t, func() { renderStoreProblems(s, 70) })
	assertColumn(t, problems, "Why", len(problemFieldIndent)+problemLabelWidth+1)
}

// The text view is the readiness answer and a pointer: the loop's other sections
// belong to the dashboard and to --json, and printing them here again is the
// surface this command gave up.
func TestStatusPrintsTheReadinessAnswerAndNothingElse(t *testing.T) {
	s := &status.SystemStatus{Posture: status.Posture{
		CommitGate: []status.ScopeArming{{Harness: "claude-code", Scope: "global"}},
		Masters:    []status.MasterRuntime{{Harness: "claude-code", Scope: "global", State: status.MasterCurrent}},
	}, Summary: status.VerdictSummary{Total: 2}}
	out := captureStdout(t, func() { renderStatus(s, reviewers.Probe{}) })
	for _, want := range []string{"Commit review", "steer dashboard", "steer status --json"} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"Arming", "Guard runner", "Problems", "Reviewer", "recorded problems\n"} {
		if strings.Contains(out, gone) {
			t.Errorf("status still prints the %q section:\n%s", gone, out)
		}
	}
}

// assertColumn reads the lines under one label and checks each continuation
// starts in the value column rather than at the margin.
func assertColumn(t *testing.T, out, label string, column int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	start := -1
	for i, line := range lines {
		// The label sits in its own column, so it is followed by padding. A section
		// heading carrying the same word is not a row.
		if strings.Contains(line, label+"  ") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no %q row in:\n%s", label, out)
	}
	// A row that follows in its own label column ends the block; a continuation
	// starts in the value column and nowhere else.
	wrapped := 0
	for _, line := range lines[start+1:] {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.TrimSpace(line) == "" || indent < column {
			break
		}
		if indent != column {
			t.Fatalf("a wrapped %s line starts in column %d, want %d: %q", label, indent, column, line)
		}
		wrapped++
	}
	if wrapped == 0 {
		t.Fatalf("%s never wrapped, so the column is untested:\n%s", label, out)
	}
}
