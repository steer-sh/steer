package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/status"
)

// keyOf builds the key message a keystroke produces.
func keyOf(name string) tea.KeyMsg {
	switch name {
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// inventory holds one record per action section, in an inventory order that is
// not the render order: the first item renders last.
func inventory() []status.ScoredIntervention {
	return []status.ScoredIntervention{
		{ID: "a", Class: "settled-problem", Status: "active", Verdict: "effective"},
		{ID: "b", Class: "waiting-problem", Status: "active", Verdict: "collecting"},
		{ID: "c", Class: "harmful-problem", Status: "active", Verdict: "harmful"},
	}
}

// The queue and the inventory are one list, so a reader answers both questions on
// one screen and the cursor walks between them.
func TestProblemsHoldTheQueueAndTheInventory(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus: "review-findings", UndecidedChains: 96,
		Dump: "steer commit-gate mine findings",
	}}
	m.active = panelProblems

	view := m.viewProblems()
	for _, want := range []string{
		headingToConsider,
		"Review findings not yet grouped into a problem",
		"/steer-report",
		"harmful-problem",
		"settled-problem",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel is missing %q:\n%s", want, view)
		}
	}
	if got := len(m.problemRows()); got != 4 {
		t.Errorf("the list holds %d rows, want the candidate plus three records", got)
	}
}

// The queue row sits above the tracked inventory: a problem nobody answered is
// the one the reader has not dealt with.
func TestTheQueueSitsAboveTheInventory(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus: "transcript-friction", Pattern: "bare python call", Count: 9, Sessions: 4,
	}}
	rows := m.problemRows()
	if rows[0].candidate == nil {
		t.Fatal("the first row is not a queue row")
	}
	for _, r := range rows[1:] {
		if r.candidate != nil {
			t.Fatal("a queue row rendered below a tracked record")
		}
	}
}

// The cursor indexes the order the panel shows, so an arrow key walks the list
// down the screen across both kinds of row.
func TestProblemsCursorWalksTheRenderedOrder(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus: "transcript-friction", Pattern: "queue-row", Count: 2, Sessions: 1,
	}}
	m.active = panelProblems

	for pos, want := range []string{"queue-row", "harmful-problem", "waiting-problem", "settled-problem"} {
		m.cursor[panelProblems] = pos
		marked := ""
		for _, line := range strings.Split(m.viewProblems(), "\n") {
			if strings.Contains(line, pointer(true)) {
				marked += strings.TrimSpace(line)
			}
		}
		if !strings.Contains(marked, want) {
			t.Errorf("cursor %d points at %q, want %q", pos, marked, want)
		}
	}
}

// Enter expands the selected entry and nothing else, and moving the selection
// closes it, so the overview stays compact.
func TestEnterExpandsOneEntryAndMovingCloses(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: []status.ScoredIntervention{{
		ID: "a", Class: "bare python call", Status: "active", Verdict: "collecting",
		Problem: "the agent runs python where this project needs python3", Tier: "guard",
		Target: "bare-python.py", Intervention: "a guard that blocks the bare command",
		Unit: "session",
	}}}
	m.active = panelProblems

	if strings.Contains(m.viewProblems(), "What goes wrong") {
		t.Fatal("the detail area is open before enter")
	}
	m.updateNav(keyOf("enter"))
	view := m.viewProblems()
	for _, want := range []string{"What goes wrong", "What you can do", "stop tracking"} {
		if !strings.Contains(view, want) {
			t.Errorf("the expanded entry is missing %q:\n%s", want, view)
		}
	}
	m.updateNav(keyOf("down"))
	if m.expanded {
		t.Error("moving the selection left the entry expanded")
	}
}

// Enter writes nothing. Authoring and deciding stay in /steer-report, and the
// panel opened a nine-field register wizard once.
func TestExpandingWritesNothing(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.snap = &status.SystemStatus{}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus: "review-findings", UndecidedChains: 96, Dump: "steer commit-gate mine findings",
	}}
	m.active = panelProblems

	for _, k := range []string{"enter", "e", "x", "t"} {
		m.panelKey(keyOf(k))
		if m.editing || m.form != nil {
			t.Fatalf("%q opened an editor over a queue row", k)
		}
	}
}

// The technical block is behind t, so a regex and a path never crowd the result.
func TestTechnicalDetailsHideBehindAKey(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: []status.ScoredIntervention{{
		ID: "bare-python", Class: "bare python call", Match: `\bpython\b`,
		Tier: "guard", Target: "/abs/path/bare-python.py", Status: "active",
		Verdict: "collecting", Unit: "session",
	}}}
	m.active = panelProblems
	m.updateNav(keyOf("enter"))

	if strings.Contains(m.viewProblems(), `\bpython\b`) {
		t.Error("the pattern is in the expanded view before t")
	}
	m.panelKey(keyOf("t"))
	view := m.viewProblems()
	for _, want := range []string{`\bpython\b`, "/abs/path/bare-python.py", "bare-python"} {
		if !strings.Contains(view, want) {
			t.Errorf("the technical block is missing %q:\n%s", want, view)
		}
	}
}

// A result the scorer withheld carries its reason in the summary. Hiding the
// reason behind enter leaves a bare badge that reads as a finding.
func TestAWithheldResultKeepsItsReasonInTheSummary(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: []status.ScoredIntervention{{
		ID: "a", Class: "bare python call", Status: "active", Verdict: "confounded",
		Unit: "session",
		Confounds: []mine.Confound{{Category: "instrument",
			Reason: "the after-window was normalized by a different parser"}},
	}}}
	m.active = panelProblems

	view := m.viewProblems()
	for _, want := range []string{"Not comparable", "different conditions", "how the evidence was produced"} {
		if !strings.Contains(view, want) {
			t.Errorf("the summary is missing %q:\n%s", want, view)
		}
	}
}

// A result with no repair names no next step. "read the counts and decide" is an
// instruction that names no decision, so it belongs on the Why line.
func TestNoNextStepWhereNoStepExists(t *testing.T) {
	it := status.ScoredIntervention{Verdict: "confounded", Unit: "chain"}
	why, next := summaryReason(it, false)
	if next != "" {
		t.Errorf("an accept named a next step: %q", next)
	}
	if why == "" {
		t.Error("an accept dropped its reason")
	}
	it = status.ScoredIntervention{Verdict: "ineffective", Unit: "chain"}
	if _, next = summaryReason(it, false); next == "" {
		t.Error("a repair named no next step")
	}
}

// The summary never claims the fix is installed and running: the record says it
// was decided on, and only the Guards panel reads what is registered now.
func TestTheSummaryClaimsNoInstallation(t *testing.T) {
	it := status.ScoredIntervention{Tier: "guard", Target: "g.py", Status: "active",
		Intervention: "blocks the bare command"}
	if got := fixSummary(it); strings.Contains(got, "installed") {
		t.Errorf("the fix line claims installation: %q", got)
	}
	if got := fixSummary(it); !strings.Contains(got, "blocks the bare command") {
		t.Errorf("the fix line does not say what the fix does: %q", got)
	}
	declined := status.ScoredIntervention{Status: "rejected", Tier: "none"}
	if got := fixLabel(declined); got != "Declined" {
		t.Errorf("a declined suggestion is labelled %q", got)
	}
}

// A proven rule is never headed as a repair.
func TestNoEffectiveRuleIsHeadedAsARepair(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	out := m.viewProblems()
	if strings.Contains(out, status.ActionHeading[status.ActionRepair]) {
		t.Fatalf("a repair section rendered over an inventory holding no repair:\n%s", out)
	}
	if !strings.Contains(out, status.ActionHeading[status.ActionSettled]) {
		t.Errorf("the effective record rendered under no settled heading:\n%s", out)
	}
}

// A refused write leaves the editor open with the typed reason. The panel closed
// the editor before it read the error, so a refusal cost the author what they typed.
func TestRefusedRetireKeepsTheReason(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.form = &retireForm{id: "no-such-record"}
	m.editing = true
	m.input.SetValue("superseded by the check")

	m.retireEditing(keyOf("enter"))

	if !m.editing || m.form == nil {
		t.Fatal("a refused write closed the stop-tracking editor")
	}
	if got := m.input.Value(); got != "superseded by the check" {
		t.Errorf("the editor lost the typed reason: %q", got)
	}
	if !strings.Contains(m.message, "error") {
		t.Errorf("the message does not report the refusal: %q", m.message)
	}
}

// Stop tracking targets the entry the pointer marks, and a queue row offers it at
// all: nothing tracks a problem nobody has answered.
func TestStopTrackingTargetsTheSelectedRecord(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.snap = &status.SystemStatus{Interventions: inventory()}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus: "transcript-friction", Pattern: "queue-row", Count: 1, Sessions: 1,
	}}
	m.active = panelProblems

	m.cursor[panelProblems] = 0
	m.panelKey(keyOf("x"))
	if m.editing {
		t.Fatal("a queue row opened the stop-tracking editor")
	}

	m.cursor[panelProblems] = 1
	m.panelKey(keyOf("x"))
	if !m.editing || m.form == nil {
		t.Fatal("the tracked record did not open the editor")
	}
	if m.form.id != "c" {
		t.Errorf("the editor opened over %q, want the harmful record %q", m.form.id, "c")
	}
}

// Long prose wraps into the terminal it has, so no line runs off the screen and
// nothing needs horizontal scrolling to read.
func TestProseWrapsToTheTerminal(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 60
	m.snap = &status.SystemStatus{Interventions: []status.ScoredIntervention{{
		ID: "a", Class: "a problem with a fairly long descriptive name", Status: "active",
		Verdict: "collecting", Unit: "session", Tier: "guard",
		Intervention: strings.Repeat("a long sentence about what this fix does. ", 6),
	}}}
	m.active = panelProblems
	for _, line := range strings.Split(m.viewProblems(), "\n") {
		if n := len([]rune(stripANSI(line))); n > m.width {
			t.Errorf("a %d-cell line overflows the %d-cell terminal: %q", n, m.width, line)
		}
	}
}

// An unbroken path or pattern is broken rather than left to overflow, so every
// character stays reachable by reading down.
func TestAnUnbrokenPathWraps(t *testing.T) {
	long := "/" + strings.Repeat("a", 120) + "/guard.py"
	for _, line := range strings.Split(strings.TrimRight(wrapTo(long, "    ", 40), "\n"), "\n") {
		if n := len([]rune(line)); n > 40 {
			t.Errorf("a %d-cell line overflows the 40-cell column: %q", n, line)
		}
	}
	if !strings.Contains(strings.ReplaceAll(wrapTo(long, "    ", 40), "\n    ", ""), long) {
		t.Error("wrapping lost part of the path")
	}
}

// The detail area pages, and says what it is hiding.
func TestALongDetailPages(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width, m.height = 80, 20
	m.snap = &status.SystemStatus{Interventions: []status.ScoredIntervention{{
		ID: "a", Class: "a problem", Status: "active", Verdict: "collecting", Unit: "session",
		Problem: strings.Repeat("a long explanation of what goes wrong. ", 20),
		Tier:    "guard", Target: "g.py",
	}}}
	m.active = panelProblems
	m.updateNav(keyOf("enter"))
	first := m.viewProblems()
	if !strings.Contains(first, "more below") {
		t.Fatalf("a detail longer than the budget does not say so:\n%s", first)
	}
	m.panelKey(keyOf("pgdown"))
	if m.detailOffset == 0 {
		t.Error("page down did not move the detail")
	}
	if !strings.Contains(m.viewProblems(), "more above") {
		t.Error("a scrolled detail does not say what is above it")
	}
	m.panelKey(keyOf("home"))
	if m.detailOffset != 0 {
		t.Errorf("home left the detail at line %d", m.detailOffset)
	}
}

// stripANSI removes the styling so a width check counts cells.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
