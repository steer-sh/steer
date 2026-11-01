package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/apikey"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// reviewerEdit is one setting's pending write.
type reviewerEdit struct {
	field settings.Field
	next  config.Config
	value string
	// choice is the highlighted row of a closed-set editor, -1 for a typed one.
	choice int
	// carry is the pending answer to what happens to the model and the effort when
	// the reviewer CLI changes: keepValues, or dropValues for the new CLI's own.
	carry string
	// asked says enter has put the question about the highlighted CLI, so an arrow answers it.
	asked bool
}

// The answers to a reviewer-CLI change. Neither is a default: a model name is
// rarely portable between CLIs, and silently keeping or silently dropping it are
// both wrong without being asked.
const (
	carryUnasked = ""
	keepValues   = "keep"
	dropValues   = "drop"
)

// reviewerKey opens the editor on the selected setting.
func (m *Model) reviewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "enter" && msg.String() != "e" {
		return m, nil
	}
	f := settings.Fields[m.cursor[panelReviewer]]
	file, err := config.Load()
	if err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.reviewer = &reviewerEdit{field: f, choice: -1}
	if f.Kind == settings.KindChoice {
		m.reviewer.choice = choiceIndex(f, f.Get(file))
	} else {
		m.input.SetValue(f.Get(file))
		m.input.Prompt = settings.LabelOf(f) + ": "
		m.input.Placeholder = f.DefaultLabel
		m.input.CursorEnd()
		m.input.Focus()
	}
	m.editing = true
	return m, nil
}

// choiceIndex is the row a stored value sits on, 0 when the value names none.
func choiceIndex(f settings.Field, value string) int {
	for i, c := range f.Choices() {
		if c.Value == value {
			return i
		}
	}
	return 0
}

// reviewerEditing drives the editor. A closed-set editor answers the arrow keys;
// a typed one takes the keystroke. Both save on enter and cancel on esc.
func (m *Model) reviewerEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := m.reviewer
	switch msg.String() {
	case "esc":
		m.closeReviewerEdit()
		m.note("cancelled")
		return m, nil
	case "enter":
		return m.commitReviewerEdit()
	}
	if ed.field.Kind == settings.KindChoice {
		switch msg.String() {
		case "up", "k":
			ed.choice--
			m.forgetCarry()
		case "down", "j":
			ed.choice++
			m.forgetCarry()
		case "left", "h":
			if ed.asked {
				ed.carry = keepValues
			}
		case "right", "l":
			if ed.asked {
				ed.carry = dropValues
			}
		}
		if n := len(ed.field.Choices()); n > 0 {
			ed.choice = (ed.choice%n + n) % n
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// forgetCarry withdraws the question and its answer, which were about the CLI the
// selection just left.
func (m *Model) forgetCarry() {
	if m.reviewer.asked {
		m.reviewer.asked, m.reviewer.carry = false, carryUnasked
		// A background result from another panel may have replaced the question.
		if m.messagePanel == panelReviewer {
			m.message = ""
		}
	}
}

// commitReviewerEdit reads the editor's value and saves it.
func (m *Model) commitReviewerEdit() (tea.Model, tea.Cmd) {
	ed := m.reviewer
	if ed.field.Kind == settings.KindChoice {
		choices := ed.field.Choices()
		if ed.choice < 0 || ed.choice >= len(choices) {
			return m, nil
		}
		return m.saveReviewerValue(choices[ed.choice].Value)
	}
	return m.saveReviewerValue(strings.TrimSpace(m.input.Value()))
}

// saveReviewerValue validates the value, asks what happens to the settings that
// travel with it, and then writes.
func (m *Model) saveReviewerValue(value string) (tea.Model, tea.Cmd) {
	ed := m.reviewer
	file, err := config.Load()
	if err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	next := file
	if err := ed.field.Set(&next, value); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	if next == file {
		m.closeReviewerEdit()
		m.note("%s is already %s", settings.LabelOf(ed.field), storedLabel(ed.field, value))
		return m, nil
	}
	// The model and the effort belong to the CLI that runs them, so a CLI change
	// asks rather than carrying them forward or dropping them unseen.
	if ed.field.Name == "reviewer.provider" && carriesValues(file) && ed.carry == carryUnasked {
		ed.asked = true
		m.note("← keep the current model and effort · → use %s's own · then enter",
			reviewers.ProviderLabel(value))
		return m, nil
	}
	if ed.carry == dropValues {
		next.Reviewer.Model = ""
		next.Reviewer.Effort = ""
	}
	ed.next, ed.value = next, value
	if err := config.Save(next); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.closeReviewerEdit()
	m.reloadReviewer()
	m.note("%s", m.savedNote(ed))
	return m, nil
}

// carriesValues says the file holds a model or an effort a CLI change would carry.
func carriesValues(file config.Config) bool {
	return file.Reviewer.Model != "" || file.Reviewer.Effort != ""
}

// storedLabel names a written value, and an unset one as the default it returns to.
func storedLabel(f settings.Field, value string) string {
	if value == "" {
		if f.DefaultLabel != "" {
			return f.DefaultLabel
		}
		return "unset, so the shipped default is used"
	}
	return value
}

// savedNote is the line the write leaves behind: the value, and the reason it may
// not yet be the value that runs.
func (m *Model) savedNote(ed *reviewerEdit) string {
	note := fmt.Sprintf("saved %s = %s", settings.LabelOf(ed.field),
		storedLabel(ed.field, ed.value))
	if ed.field.Name == "reviewer.timeout" {
		// The engine reads the new value on the next firing; the coding agent's own
		// hook clock is a registered number, and a review killed by the smaller
		// clock lands the commit unreviewed with no record at all.
		return note + " — " + timeoutFollowUp(m.snap)
	}
	if source := settings.Source(ed.field, ed.next); strings.HasPrefix(source, "env ") {
		return note + fmt.Sprintf(" — %s is set and outranks this file, so what runs is unchanged",
			strings.TrimPrefix(source, "env "))
	}
	return note
}

// timeoutFollowUp names the registrations whose own clock this write does not
// move, and the operation that moves them.
func timeoutFollowUp(snap *status.SystemStatus) string {
	var where []string
	if snap != nil {
		for _, a := range snap.Posture.CommitGate {
			if a.Registered {
				where = append(where, armingLabel(a))
			}
		}
	}
	if len(where) == 0 {
		return "the engine uses it on the next review"
	}
	return "the engine uses it on the next review, but " + strings.Join(where, ", ") +
		" still carries its own clock until you run: steer commit-gate install"
}

// closeReviewerEdit leaves the editor and clears the one text input the panels
// share, so the next editor to open sets its own prompt over nothing.
func (m *Model) closeReviewerEdit() {
	m.editing = false
	m.reviewer = nil
	m.input.SetValue("")
	m.input.Prompt = ""
	m.input.Placeholder = ""
}

// reloadReviewer recomposes the snapshot under the configuration just written, so the
// panel's in-force column moves with the write.
func (m *Model) reloadReviewer() {
	// Resolve, not Load: the in-force column names the model that would actually
	// run, which for an unset model is the provider's own label.
	if loaded, err := gate.Load(); err == nil {
		loaded.StateDir = m.cfg.StateDir
		// An API reviewer whose URL is not written yet does not resolve, and its
		// panel must still show the values just written.
		cfg, _, _ := settings.Resolve(loaded, nil)
		m.cfg = cfg
	}
	m.refresh()
}

// viewReviewer renders the settings, the help for the one selected, the editor
// when it is open, and any registration that outranks the file.
func (m *Model) viewReviewer() string {
	width := m.bodyWidth()
	out := m.title("Reviewer — settings for review before commit")
	out += wrapTo(secondary("These are saved for every project on this machine. An environment "+
		"variable, or a flag left on a registration, can still outrank them."), "  ", width)
	file, err := config.Load()
	if err != nil {
		out += errText(fmt.Sprintf("  %v\n", err))
		file = config.Config{}
	}
	out += "\n"
	for i, f := range settings.Fields {
		selected := i == m.cursor[panelReviewer]
		out += selectedRow(reviewerLabel(settings.LabelOf(f), selected), " "+settings.DisplayValue(f, m.cfg), selected && !m.editing, width)
		if override := overrideNote(f, file); override != "" {
			out += wrapTo(warn("Override: ")+override, "    ", width)
		}
		if selected && !m.editing {
			out += wrapTo(secondary(f.Help), "    ", width)
		}
	}
	out += m.viewReviewerKey(width)

	if m.editing && m.reviewer != nil {
		out += inset(m.viewReviewerEditor(file, width-3), width, true)
	}

	if pinned := m.pinnedReviewerRows(); len(pinned) > 0 {
		out += section("Registrations that outrank this file")
		for _, row := range pinned {
			out += wrapTo(warn(row.label)+" "+row.command, "   ", width)
		}
		out += wrapTo(dim("A reviewer named on a registration wins over this file, so an edit "+
			"here changes nothing for it. Repair with: steer bin install"), "   ", width)
	}
	return out + m.viewReviewerDetails(width)
}

// viewReviewerKey is the read-only key row an API reviewer adds. The dashboard takes
// no key: a key typed here would pass through the terminal the agent may be reading.
func (m *Model) viewReviewerKey(width int) string {
	p, ok := reviewers.FindProvider(m.cfg.ReviewerProvider)
	if !ok || !p.API {
		return ""
	}
	_, st := apikey.Find(m.cfg.ReviewerAPIKeyEnv)
	out := selectedRow(reviewerLabel("API key", false), " "+st.Label(), false, width)
	return out + wrapTo(dim("Set it in your own terminal: "+apikey.SetCommand), "    ", width)
}

// viewReviewerDetails is where the file itself and its stored key names live. A
// reader needs them to edit the file by hand or from another machine; nobody needs
// them to change a setting here.
func (m *Model) viewReviewerDetails(width int) string {
	out := section("Details")
	out += wrapTo(dim("Saved in "+configPath()), "  ", width)
	names := make([]string, 0, len(settings.Fields))
	for _, f := range settings.Fields {
		names = append(names, f.Name)
	}
	return out + wrapTo(dim("Keys: "+strings.Join(names, ", ")), "  ", width)
}

// overrideNote names an environment variable in force over one key, and the
// stored value it hides. Where nothing overrides, the row's one value is the
// stored one and the in-force one at once, so the note is silent.
func overrideNote(f settings.Field, file config.Config) string {
	if f.Env == "" || !strings.HasPrefix(settings.Source(f, file), "env ") {
		return ""
	}
	stored := settings.StoredValue(f, file)
	if stored == "" {
		return f.Env + " is set in your environment and supplies this value"
	}
	return fmt.Sprintf("%s is set in your environment and supplies this value; "+
		"this file holds %s", f.Env, stored)
}

// viewReviewerEditor renders the open editor: a list for a closed set, a text
// field otherwise, plus what the value means and what saving it costs.
func (m *Model) viewReviewerEditor(file config.Config, width int) string {
	ed := m.reviewer
	out := section("Edit " + settings.LabelOf(ed.field))
	if ed.field.Kind == settings.KindChoice {
		for i, c := range ed.field.Choices() {
			out += selectedRow(c.Label, "", i == ed.choice, width)
		}
	} else {
		m.input.Width = max(1, width-6)
		out += selectedRow(m.input.View(), "", true, width)
	}
	out += wrapTo(secondary(ed.field.Help), "  ", width)
	if ed.field.Suggestions != nil {
		if levels := ed.field.Suggestions(m.cfg); len(levels) > 0 {
			out += wrapTo(dim("This CLI documents "+strings.Join(levels, ", ")+
				". Another value is passed through unchanged."), "  ", width)
		}
	}
	out += wrapTo(dim(editorHint(ed.field)), "  ", width)
	if strings.HasPrefix(settings.Source(ed.field, file), "env ") {
		out += wrapTo(warn("Override: ")+"This saves the file. "+ed.field.Env+
			" is set in your environment and outranks it, so what runs will not change.",
			"  ", width)
	}
	if ed.carry != carryUnasked {
		out += wrapTo(carryNote(ed.carry), "  ", width)
	}
	return out
}

// editorHint names the keys this editor answers and the value that clears it.
func editorHint(f settings.Field) string {
	switch f.Kind {
	case settings.KindChoice:
		return "↑↓ choose · enter save · esc cancel"
	case settings.KindNumber:
		return "type a whole number of " + f.Unit + "s · enter save · esc cancel"
	}
	if f.DefaultLabel != "" {
		return "enter save · empty means " + strings.ToLower(f.DefaultLabel) + " · esc cancel"
	}
	return "enter save · esc cancel"
}

// carryNote says what the pending answer does to the model and the effort.
func carryNote(carry string) string {
	if carry == dropValues {
		return "The model and the effort will be cleared, so the new CLI uses its own."
	}
	return "The current model and effort will be kept. A model name from another CLI " +
		"may not be one this CLI accepts."
}

// pinnedReviewerRow is one registered commit-gate command still naming the reviewer.
type pinnedReviewerRow struct {
	label   string
	command string
}

// pinnedReviewerRows reads the commit-gate registrations in the snapshot for a
// reviewer flag. A flag outranks this file, so a registration carrying one pins the
// reviewer that install named and every edit here changes nothing.
func (m *Model) pinnedReviewerRows() []pinnedReviewerRow {
	if m.snap == nil {
		return nil
	}
	var rows []pinnedReviewerRow
	for _, a := range m.snap.Posture.CommitGate {
		if a.Registered && steerbin.HasFlags(a.Command, settings.ReviewerFlags...) {
			rows = append(rows, pinnedReviewerRow{label: armingLabel(a), command: a.Command})
		}
	}
	return rows
}

// armingLabel names a registration the way its coding agent and location read.
func armingLabel(a status.ScopeArming) string {
	if a.Harness == "" {
		return scopeReach(a.Scope)
	}
	return status.HarnessLabel(a.Harness) + ", " + scopeReach(a.Scope)
}

// configPath is where the settings file lives, for the technical block.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.steer/config.json"
	}
	return home + "/.steer/config.json"
}

func reviewerLabel(label string, selected bool) string {
	text := fmt.Sprintf("%-18s", label)
	if selected {
		return text
	}
	return secondary(text)
}
