package tui

import (
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
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
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The panel is the same editor as `steer config`, so it lists the same keys. It
// names each in the reader's words and keeps the stored key in the details, where
// somebody editing the file by hand still finds it.
func TestReviewerPanelNamesEverySetting(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.active = panelReviewer
	view := m.View()
	for _, f := range settings.Fields {
		if !strings.Contains(view, settings.LabelOf(f)) {
			t.Errorf("the reviewer panel omits %s", settings.LabelOf(f))
		}
		if !strings.Contains(view, f.Name) {
			t.Errorf("the details omit the stored key %s", f.Name)
		}
	}
}

// An edit writes through config.Save, the one writer `steer config set` calls, so a
// value set here is the value that command reads back.
func TestReviewerPanelWritesTheSettingsFile(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.model")

	m.Update(key("enter"))
	m.input.SetValue("gemini-3.7-flash")
	m.Update(key("enter"))

	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Model != "gemini-3.7-flash" {
		t.Fatalf("stored model %q, want gemini-3.7-flash", got.Reviewer.Model)
	}
	if m.editing {
		t.Error("the editor is still open after a write")
	}
}

// The reviewer CLI is a closed set, so the editor offers the set rather than
// asking the reader to type a name that could be a typo. The set comes from the
// one provider definition, so the panel can never offer one the gate refuses.
func TestReviewerPanelChoosesTheCLIFromAList(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.provider")

	m.Update(key("enter"))
	view := m.View()
	for _, p := range reviewers.Providers() {
		if !strings.Contains(view, p.Label) {
			t.Errorf("the editor omits the reviewer CLI %q", p.Label)
		}
	}

	m.Update(key("down"))
	m.Update(key("enter"))

	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Provider != "codex" {
		t.Fatalf("stored provider %q, want the second row, codex", got.Reviewer.Provider)
	}
}

// A CLI change asks what happens to the model and the effort it carried. Neither
// answer is a default: a model name is rarely one the new CLI accepts, and
// keeping or dropping it unasked are both wrong.
func TestChangingTheCLIAsksAboutTheModelAndTheEffort(t *testing.T) {
	tempSettingsHome(t)
	if err := config.Save(config.Config{Reviewer: config.Reviewer{
		Provider: "agy", Model: "gemini-3.7-flash", Effort: "high"}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.provider")

	m.Update(key("enter"))
	m.Update(key("down"))
	m.Update(key("enter"))

	if got, _ := config.Load(); got.Reviewer.Provider != "agy" {
		t.Fatalf("the CLI changed before the question was answered: %q", got.Reviewer.Provider)
	}
	if !strings.Contains(m.message, "keep the current model") {
		t.Errorf("the panel does not ask about the model and the effort: %q", m.message)
	}

	m.Update(key("right"))
	m.Update(key("enter"))
	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Provider != "codex" {
		t.Fatalf("stored provider %q, want codex", got.Reviewer.Provider)
	}
	if got.Reviewer.Model != "" || got.Reviewer.Effort != "" {
		t.Errorf("the new CLI kept %q / %q, want its own defaults",
			got.Reviewer.Model, got.Reviewer.Effort)
	}
}

// An explicit action returns a key to its default, so nobody has to learn that an
// empty field clears it.
func TestReviewerPanelReturnsAFieldToItsDefault(t *testing.T) {
	tempSettingsHome(t)
	if err := config.Save(config.Config{Reviewer: config.Reviewer{Model: "some-model"}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.model")

	m.Update(key("enter"))
	m.input.SetValue("")
	m.Update(key("d"))

	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Model != "" {
		t.Errorf("the model is still %q", got.Reviewer.Model)
	}
	if !strings.Contains(m.message, "Steer default") {
		t.Errorf("the note does not name the default: %q", m.message)
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
	for _, want := range []string{"Registrations that outrank this file",
		"Claude Code, every project on this machine", "steer bin install"} {
		if !strings.Contains(view, want) {
			t.Errorf("the reviewer panel is missing %q", want)
		}
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
