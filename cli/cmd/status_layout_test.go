package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/status"
)

// A value that runs past the terminal continues under its own column. Printing
// the continuation at column zero puts prose where a label belongs, which is what
// the dashboard's stacked labels avoid.
func TestEveryWrappedLineLandsInItsOwnColumn(t *testing.T) {
	long := strings.Repeat("a long sentence that has to wrap more than once. ", 6)
	s := &status.SystemStatus{
		Posture: status.Posture{Reviewer: long},
		Attention: status.AttentionQueue{Interventions: []cadence.Intervention{{
			Class:   "a recurring problem",
			Problem: long,
			Tier:    "check",
			Target:  "check.sh",
			Verdict: "collecting",
			Unit:    "chain",
		}}},
	}

	posture := captureStdout(t, func() { renderPosture(s, 70) })
	assertColumn(t, posture, "Reviewer", 2+postureLabelWidth+1)

	problems := captureStdout(t, func() { renderAttention(s, 70) })
	assertColumn(t, problems, "Problem", len(problemFieldIndent)+problemLabelWidth+1)
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
