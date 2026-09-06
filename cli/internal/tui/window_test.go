package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestShortTerminalKeepsTheSelectionAndTheLegend pins that a panel taller than the
// terminal still shows the row the cursor is on, and still shows the key legend.
// The renderer emitted the whole inventory, so the cursor moved off screen and
// enter acted on a row the reader could not see.
func TestShortTerminalKeepsTheSelectionAndTheLegend(t *testing.T) {
	m := New(gate.Defaults(), "global")
	rows := make([]status.ScopeArming, 0, 24)
	for i := 0; i < 24; i++ {
		rows = append(rows, status.ScopeArming{
			Harness: fmt.Sprintf("harness-%02d", i), Scope: "global"})
	}
	m.snap = &status.SystemStatus{Posture: status.Posture{CommitGate: rows}}
	m.active = panelChannels
	m.height = 20

	m.cursor[panelChannels] = 21
	view := m.View()

	if !strings.Contains(view, "harness-21") {
		t.Error("the selected row is off screen")
	}
	if !strings.Contains(view, "tab/←→ panel") {
		t.Error("the key legend was pushed off screen")
	}
	if !strings.Contains(view, "more above") {
		t.Error("the window does not say that rows are hidden")
	}
	if n := strings.Count(view, "\n"); n > m.height {
		t.Errorf("the frame is %d lines, over the %d-line terminal", n, m.height)
	}
}

// TestNoPanelOverflowsTheTerminal pins the budget against every panel at several
// heights. A panel body may or may not end with a newline, and the chrome adds its
// own separator, so the arithmetic is checked by measurement rather than by
// reading it: an off-by-one pushes the key legend off the screen.
func TestNoPanelOverflowsTheTerminal(t *testing.T) {
	m := New(gate.Defaults(), "global")
	var gates []status.ScopeArming
	var cands []cadence.Candidate
	var scored []status.ScoredIntervention
	for i := 0; i < 30; i++ {
		gates = append(gates, status.ScopeArming{Harness: "claude-code", Scope: fmt.Sprint(i)})
		cands = append(cands, cadence.Candidate{Pattern: fmt.Sprintf("p%02d", i), Corpus: "review-findings"})
		scored = append(scored, status.ScoredIntervention{ID: fmt.Sprint(i), Class: "c", Match: "m",
			Tier: "check", Target: "check.sh", Status: "active", Verdict: "collecting"})
	}
	m.snap = &status.SystemStatus{
		Posture:       status.Posture{CommitGate: gates},
		Attention:     status.AttentionQueue{Candidates: cands},
		Interventions: scored,
	}

	for _, height := range []int{12, 18, 24} {
		m.height = height
		for _, p := range []panel{panelChannels, panelProblems, panelGuards} {
			m.active, m.cursor[p] = p, 20
			frame := m.View()
			if n := len(strings.Split(strings.TrimRight(frame, "\n"), "\n")); n > height {
				t.Errorf("panel %d at height %d rendered %d lines", p, height, n)
			}
			if !strings.Contains(frame, "q quit") {
				t.Errorf("panel %d at height %d lost the key legend", p, height)
			}
		}
	}
}

// TestWindowShowsTheSelectedEntryNotJustItsFirstLine pins that a multi-line entry
// stays readable. A row is the first line of its entry, so a window that ends on
// the selection would cut off everything the entry says.
func TestWindowShowsTheSelectedEntryNotJustItsFirstLine(t *testing.T) {
	var b strings.Builder
	b.WriteString(testHeader("Problems"))
	const selected = 20
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "%s class-%02d\n", pointer(i == selected), i)
		fmt.Fprintf(&b, "    verdict line for %02d\n", i)
		fmt.Fprintf(&b, "    scope line for %02d\n", i)
	}

	got := windowBody(b.String(), 12)
	for _, want := range []string{
		fmt.Sprintf("class-%02d", selected),
		fmt.Sprintf("verdict line for %02d", selected),
		fmt.Sprintf("scope line for %02d", selected),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the window cut %q off the selected entry", want)
		}
	}
}

// TestWindowKeepsTheBodyEnding pins that windowing does not add a line ending the
// panel did not have. The chrome puts its own separator after the body, so an
// added newline costs one line of the terminal.
func TestWindowKeepsTheBodyEnding(t *testing.T) {
	var b strings.Builder
	b.WriteString(testHeader("Panel"))
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "%s row-%02d\n", pointer(i == 0), i)
	}
	withNewline := b.String()
	withoutNewline := strings.TrimRight(withNewline, "\n")

	if got := windowBody(withNewline, 10); !strings.HasSuffix(got, "\n") {
		t.Error("the window dropped the body's trailing newline")
	}
	if got := windowBody(withoutNewline, 10); strings.HasSuffix(got, "\n") {
		t.Error("the window added a trailing newline the body did not have")
	}
}

// TestUnknownHeightNeverWindows pins that a body is whole until the harness reports
// a size. Dropping rows on an unknown terminal is worse than overflowing it.
func TestUnknownHeightNeverWindows(t *testing.T) {
	body := strings.Repeat("row\n", 50)
	if got := windowBody(body, 0); got != body {
		t.Error("a zero budget windowed the body")
	}
}

// TestWindowFollowsTheSelectionBothWays pins that the window moves to the cursor
// from either direction and always reports what it hid.
func TestWindowFollowsTheSelectionBothWays(t *testing.T) {
	build := func(sel int) string {
		var b strings.Builder
		b.WriteString(testHeader("Panel"))
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&b, "%s row-%02d\n", pointer(i == sel), i)
		}
		return b.String()
	}
	for _, sel := range []int{0, 15, 29} {
		got := windowBody(build(sel), 10)
		want := fmt.Sprintf("row-%02d", sel)
		if !strings.Contains(got, want) {
			t.Errorf("selection %d: the window does not hold %q", sel, want)
		}
		if !strings.Contains(got, "Panel") {
			t.Errorf("selection %d: the panel header scrolled away", sel)
		}
		if n := strings.Count(strings.TrimRight(got, "\n"), "\n") + 1; n > 10 {
			t.Errorf("selection %d: the window is %d lines, over the 10 budgeted", sel, n)
		}
	}
}

// testHeader is a panel header with the blank line under it, which is the shape
// windowBody reads its sticky region off.
func testHeader(s string) string { return s + "\n\n" }
