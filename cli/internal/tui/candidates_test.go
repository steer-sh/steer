package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestCandidatesPanelWritesNothing pins that the panel is read-only. It carried a
// nine-field register wizard that asked the reader to author a class name, a regex
// and its prose with the evidence hidden behind the form. /steer-report does that
// work with the findings in context, so no key here opens an editor.
func TestCandidatesPanelWritesNothing(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.snap = &status.SystemStatus{}
	m.snap.Attention.Candidates = []cadence.Candidate{{
		Corpus:          "review-findings",
		UndecidedChains: 96,
		Dump:            "steer commit-gate mine findings",
	}}
	m.active = panelCandidates

	for _, key := range []string{"enter", "e", "x"} {
		m.panelKey(keyOf(key))
		if m.editing || m.form != nil {
			t.Fatalf("%q opened an editor on the candidates panel", key)
		}
	}

	view := m.viewCandidates()
	if !strings.Contains(view, "/steer-report") {
		t.Errorf("the panel does not name the skill that decides these:\n%s", view)
	}
}

// keyOf builds the key message a keystroke produces.
func keyOf(name string) tea.KeyMsg {
	switch name {
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}
