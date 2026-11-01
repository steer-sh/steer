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

// openCarryingEditor stores a model and an effort under agy and opens the named
// setting's editor.
func openCarryingEditor(t *testing.T, field string) *Model {
	t.Helper()
	tempSettingsHome(t)
	if err := config.Save(config.Config{Reviewer: config.Reviewer{
		Provider: "agy", Model: "gemini-3.7-flash", Effort: "high"}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, field)
	m.Update(key("enter"))
	return m
}

// assertCarried requires the stored provider, with the model and the effort intact.
func assertCarried(t *testing.T, provider string) {
	t.Helper()
	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Provider != provider {
		t.Errorf("stored provider %q, want %q", got.Reviewer.Provider, provider)
	}
	if got.Reviewer.Model != "gemini-3.7-flash" || got.Reviewer.Effort != "high" {
		t.Errorf("stored model and effort %q / %q, want both kept",
			got.Reviewer.Model, got.Reviewer.Effort)
	}
}

// An arrow pressed before the question appears answers nothing, so the CLI change
// still asks.
func TestAnArrowBeforeTheQuestionAnswersNothing(t *testing.T) {
	for name, keys := range map[string][]string{
		"before choosing": {"right", "down", "enter"},
		"after choosing":  {"down", "h", "enter"},
	} {
		t.Run(name, func(t *testing.T) {
			m := openCarryingEditor(t, "reviewer.provider")
			for _, k := range keys {
				m.Update(key(k))
			}
			assertCarried(t, "agy")
			if !strings.Contains(m.message, "keep the current model") {
				t.Errorf("the panel does not ask: %q", m.message)
			}
			if view := m.View(); strings.Contains(view, carryNote(dropValues)) ||
				strings.Contains(view, carryNote(keepValues)) {
				t.Error("the editor states an answer nobody gave")
			}
		})
	}
}

// An answer belongs to the CLI it was given for, so moving to another asks again
// and the question naming the old one leaves.
func TestMovingToAnotherCLIDiscardsTheAnswer(t *testing.T) {
	m := openCarryingEditor(t, "reviewer.provider")
	m.Update(key("down"))
	m.Update(key("enter"))
	m.Update(key("right"))
	m.Update(key("down"))

	if m.message != "" {
		t.Errorf("the question about the previous CLI is still showing: %q", m.message)
	}
	if strings.Contains(m.View(), carryNote(dropValues)) {
		t.Error("the editor still states the answer given for the previous CLI")
	}

	m.Update(key("enter"))
	assertCarried(t, "agy")
	third := reviewers.Providers()[2]
	if !strings.Contains(m.message, reviewers.ProviderLabel(third.ID)) {
		t.Errorf("the panel does not ask about %s: %q", third.ID, m.message)
	}

	m.Update(key("left"))
	m.Update(key("enter"))
	assertCarried(t, third.ID)
}

// Moving the selection withdraws the Reviewer panel's question, never a result
// another panel filed over it while the editor was open.
func TestMovingTheSelectionKeepsAnotherPanelsResult(t *testing.T) {
	m := openCarryingEditor(t, "reviewer.provider")
	m.Update(key("down"))
	m.Update(key("enter"))
	m.Update(key("down"))
	if m.message != "" {
		t.Errorf("the question stayed after the selection moved: %q", m.message)
	}

	m.Update(key("enter"))
	if !m.reviewer.asked {
		t.Fatal("the third CLI did not ask")
	}
	m.noteFor(panelChannels, "outcomes read")
	m.Update(key("down"))
	if m.message != "outcomes read" {
		t.Errorf("the other panel's result became %q", m.message)
	}
}

// Returning to the current CLI with a drop answer pending changes nothing.
//
// pins existing behaviour
func TestReturningToTheCurrentCLIKeepsTheValues(t *testing.T) {
	m := openCarryingEditor(t, "reviewer.provider")
	for _, k := range []string{"down", "enter", "right", "k", "enter"} {
		m.Update(key(k))
	}
	assertCarried(t, "agy")
	if m.editing {
		t.Error("the editor is still open on the current value")
	}
}

// Another closed-set setting has no carry question, so an arrow in its editor
// cannot clear the model and the effort.
func TestAnArrowInAnotherChoiceFieldKeepsTheValues(t *testing.T) {
	m := openCarryingEditor(t, "reviewer.api.tools")
	for _, k := range []string{"right", "down", "enter"} {
		m.Update(key(k))
	}
	assertCarried(t, "agy")
	if got, _ := config.Load(); got.Reviewer.API.Tools == "" {
		t.Error("the setting itself was not saved")
	}
}

// Saving an empty field returns a key to its default, and the open editor says so.
func TestReviewerPanelReturnsAFieldToItsDefault(t *testing.T) {
	tempSettingsHome(t)
	if err := config.Save(config.Config{Reviewer: config.Reviewer{Model: "some-model"}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.width = 200
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.model")

	m.Update(key("enter"))
	if hint := "empty means steer default"; !strings.Contains(m.View(), hint) {
		t.Errorf("the editor does not show %q", hint)
	}
	m.input.SetValue("")
	m.Update(key("enter"))

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

// A typed name may begin with any letter, including on a field that starts empty.
func TestReviewerEditorTypesANameStartingWithD(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.active = panelReviewer
	m.cursor[panelReviewer] = indexOfField(t, "reviewer.model")

	m.Update(key("enter"))
	for _, r := range "deepseek-r1" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))

	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Reviewer.Model != "deepseek-r1" {
		t.Errorf("the model is %q, want deepseek-r1", got.Reviewer.Model)
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
