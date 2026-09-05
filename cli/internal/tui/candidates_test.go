package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
)

// setStep writes a wizard step by label, and reports whether the step exists.
func setStep(m *Model, label, value string) bool {
	for i := range m.form.steps {
		if m.form.steps[i].label == label {
			m.form.steps[i].value = value
			return true
		}
	}
	return false
}

// TestFailedWriteKeepsTheForm pins that a refused write leaves the wizard open with
// every typed value. The panel closed the editor before it read the error, so a
// refusal the author could correct in one field cost them the whole entry.
func TestFailedWriteKeepsTheForm(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.startForm(formRegister, cadence.Candidate{Pattern: "bare python"})

	for _, kv := range [][2]string{
		{"class", "bare python call"},
		{"match", "python "},
		{"tier", "guard"}, // a guard tier naming no harness is refused
		{"target", "guard.py"},
	} {
		if !setStep(m, kv[0], kv[1]) {
			t.Fatalf("the register wizard has no %q step", kv[0])
		}
	}
	m.form.idx = len(m.form.steps) - 1

	m.commitForm()

	if m.form == nil || !m.editing {
		t.Fatal("a refused write closed the wizard")
	}
	if !strings.Contains(m.message, "--scope-harness") {
		t.Errorf("the message does not name the guard-tier refusal: %q", m.message)
	}
	for _, kv := range [][2]string{{"class", "bare python call"}, {"match", "python "}, {"tier", "guard"}} {
		got := ""
		for _, s := range m.form.steps {
			if s.label == kv[0] {
				got = s.value
			}
		}
		if got != kv[1] {
			t.Errorf("step %q = %q after the refusal, want %q", kv[0], got, kv[1])
		}
	}
}

// TestShiftTabStepsBackAndKeepsTheValue pins that the wizard can return to an
// earlier field, carrying what is typed in the field it leaves.
func TestShiftTabStepsBackAndKeepsTheValue(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.cfg.StateDir = t.TempDir()
	m.startForm(formRegister, cadence.Candidate{Pattern: "bare python"})
	m.form.idx = 1 // the match step
	m.loadStep()
	m.input.SetValue("python3? ")

	m.updateEditing(keyOf("shift+tab"))

	if m.form.idx != 0 {
		t.Fatalf("shift+tab left the wizard on step %d, want 0", m.form.idx)
	}
	if got := m.form.steps[1].value; got != "python3?" {
		t.Errorf("the match step lost its value on the way back: %q", got)
	}

	// At the first step, shift+tab holds rather than wrapping to the last.
	m.updateEditing(keyOf("shift+tab"))
	if m.form.idx != 0 {
		t.Errorf("shift+tab wrapped past the first step to %d", m.form.idx)
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
