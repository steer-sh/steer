package tui

import (
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
)

// tempSettingsHome points the settings file at a temporary home, so a test never
// reads or rewrites the operator's own configuration.
func tempSettingsHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The panel is the same editor as `steer config`, so it lists the same keys.
func TestReviewerPanelNamesEverySetting(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	view := m.View()
	for _, name := range settings.Names() {
		if !strings.Contains(view, name) {
			t.Errorf("the reviewer panel omits %s", name)
		}
	}
}

// An edit writes through config.Save, the one writer `steer config set` calls, so a
// value set here is the value that command reads back.
func TestReviewerPanelWritesTheSettingsFile(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.provider")

	m.Update(key("enter"))
	m.input.SetValue("codex")
	m.Update(key("enter"))

	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Provider != "codex" {
		t.Fatalf("stored provider %q, want codex", got.Reviewer.Provider)
	}
	if m.editing {
		t.Error("the editor is still open after a write")
	}
}

// The provider set is closed, so a typo is refused at write time rather than left to
// fail open on every commit.
func TestReviewerPanelRefusesAnUnknownProvider(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.provider")

	m.Update(key("enter"))
	m.input.SetValue("gpt")
	m.Update(key("enter"))

	if !strings.Contains(m.message, "unknown reviewer provider") {
		t.Errorf("message %q does not name the refusal", m.message)
	}
	if got, err := config.Load(); err != nil || got != (config.Config{}) {
		t.Fatalf("a refused write left %#v (err %v); want an untouched file", got, err)
	}
}

// A flag on a registration outranks the settings file, so an edit here changes
// nothing until it is stripped. The panel has to say so.
func TestReviewerPanelNamesAPinnedRegistration(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Posture: status.Posture{CommitGate: []status.ScopeArming{{
		Harness: "claude-code", Scope: "global", Registered: true,
		Command: "steer commit-gate review --provider agy --model gemini-3.7-flash-high",
	}}}}
	m.active = panelReviewer
	view := m.View()
	for _, want := range []string{"Registrations that outrank this file", "claude-code global scope", "steer bin install"} {
		if !strings.Contains(view, want) {
			t.Errorf("the reviewer panel is missing %q", want)
		}
	}
}

// An identity change moves the instrument the ledger stamps, so the panel names the
// measurements it lands in and takes a second enter before it writes.
func TestReviewerPanelConfirmsAnIdentityChangeThatLandsInAMeasurement(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.impactOf = func(gate.Config) []mine.InstrumentImpact {
		return []mine.InstrumentImpact{{ID: "bare-python", Class: "bare python call", Grade: mine.DriftMajor}}
	}
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.effort")

	m.Update(key("enter"))
	m.input.SetValue("high")
	m.Update(key("enter"))

	if got, err := config.Load(); err != nil || got != (config.Config{}) {
		t.Fatalf("the first enter wrote %#v (err %v); want the confirmation first", got, err)
	}
	view := m.View()
	for _, want := range []string{"lands in 1 measurement(s)", "bare python call", "the verdict is withheld"} {
		if !strings.Contains(view, want) {
			t.Errorf("the confirmation is missing %q", want)
		}
	}

	m.Update(key("enter"))
	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Effort != "high" {
		t.Fatalf("stored effort %q, want high after the confirmation", got.Reviewer.Effort)
	}
}

func indexOfField(t *testing.T, name string) int {
	t.Helper()
	for i, f := range settings.Fields {
		if f.Name == name {
			return i
		}
	}
	t.Fatalf("no setting named %s", name)
	return 0
}
