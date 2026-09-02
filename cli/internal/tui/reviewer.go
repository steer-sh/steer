package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// reviewerEdit is one field's pending write. A change to the reviewer's identity
// moves the instrument the ledger stamps, so the panel names the measurements it
// lands in and takes a second enter before it writes — the order `steer config set`
// prints in.
type reviewerEdit struct {
	field   settings.Field
	next    config.Config
	value   string
	impacts []mine.InstrumentImpact
	confirm bool
}

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
	m.reviewer = &reviewerEdit{field: f}
	m.input.SetValue(f.Get(file))
	m.input.Prompt = f.Name + " = "
	m.input.Placeholder = f.Usage
	m.input.CursorEnd()
	m.input.Focus()
	m.editing = true
	m.matchCount = ""
	return m, nil
}

// reviewerEditing drives the editor: enter validates and then writes, esc cancels.
func (m *Model) reviewerEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeReviewerEdit()
		m.note("cancelled")
		return m, nil
	case "enter":
		return m.commitReviewerEdit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// A keystroke invalidates the confirmation: the impacts on screen were graded
	// against the value the operator has just moved away from.
	m.reviewer.confirm = false
	return m, cmd
}

// commitReviewerEdit validates the typed value, then reports the measurements the
// change lands in before it writes.
func (m *Model) commitReviewerEdit() (tea.Model, tea.Cmd) {
	ed := m.reviewer
	value := strings.TrimSpace(m.input.Value())
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
		m.note("%s is already %s", ed.field.Name, settings.StoredLabel(value))
		return m, nil
	}
	ed.next, ed.value = next, value
	if ed.field.Identity && !ed.confirm {
		if cfg, err := settings.Preview(next, nil); err == nil {
			ed.impacts = m.impactOf(cfg)
		}
		if len(ed.impacts) > 0 {
			ed.confirm = true
			m.note("enter again to write, esc to cancel")
			return m, nil
		}
	}
	if err := config.Save(next); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.closeReviewerEdit()
	m.reloadReviewer()
	m.note("%s", m.savedNote(ed))
	return m, nil
}

// savedNote is the line the write leaves behind: the value, and the reason it may
// not yet be the value that runs.
func (m *Model) savedNote(ed *reviewerEdit) string {
	note := fmt.Sprintf("%s = %s", ed.field.Name, settings.StoredLabel(ed.value))
	if ed.field.Name == "reviewer.timeout" {
		// The engine reads the new value on the next firing; the harness's own hook
		// timeout is a registered number, and a review killed by the smaller wall
		// clock lands the commit ungated with no row at all.
		return note + " — re-run `steer commit-gate install` for the hook's own wall clock"
	}
	if source := settings.Source(ed.field, ed.next); strings.HasPrefix(source, "env ") {
		return note + fmt.Sprintf(" — %s is set and outranks the file here",
			strings.TrimPrefix(source, "env "))
	}
	return note
}

// closeReviewerEdit leaves the editor and restores the candidate workbench's prompt,
// which shares the one text input.
func (m *Model) closeReviewerEdit() {
	m.editing = false
	m.reviewer = nil
	m.input.SetValue("")
	m.input.Prompt = "match /"
	m.input.Placeholder = "regex for this class"
}

// reloadReviewer recomposes the snapshot under the configuration just written, so the
// panel's effective column and the chrome's instrument line both move with the write.
func (m *Model) reloadReviewer() {
	// Resolve, not Load: the effective column names the model that would actually
	// run, which for an unset model is the provider's own label.
	if loaded, err := gate.Load(); err == nil {
		loaded.StateDir = m.cfg.StateDir
		cfg, _, err := settings.Resolve(loaded, nil)
		if err == nil {
			m.cfg = cfg
		}
	}
	m.refresh()
}

// viewReviewer renders the settings, the layer each value comes from, and any
// registration that outranks the file.
func (m *Model) viewReviewer() string {
	out := header("Reviewer — the CLI that red-teams a diff (~/.steer/config.json)")
	file, err := config.Load()
	if err != nil {
		out += errText(fmt.Sprintf("  %v\n", err))
		file = config.Config{}
	}
	for i, f := range settings.Fields {
		out += fmt.Sprintf("%s %-18s %-24s %s\n", pointer(i == m.cursor[panelReviewer]),
			f.Name, f.Effective(m.cfg), dim(settings.Source(f, file)))
	}
	out += dim("  a flag outranks this file, the environment outranks it, and the file " +
		"outranks the shipped defaults.\n")

	if m.editing && m.reviewer != nil {
		out += section("Edit")
		out += "  " + m.input.View() + "\n"
		out += "  " + dim(m.reviewer.field.Usage+" · empty clears the key") + "\n"
		out += m.viewReviewerImpact()
	}

	if pinned := m.pinnedReviewerRows(); len(pinned) > 0 {
		out += section("Registrations that outrank this file")
		for _, row := range pinned {
			out += warn(fmt.Sprintf("   %-22s %s\n", row.label, truncate(row.command, 60)))
		}
		out += dim("   repair: steer bin install · steer commit-gate status reads every channel.\n")
	}
	return out
}

// viewReviewerImpact names the measurements the pending change lands in, graded by
// the scorer's own computation rather than by a second opinion about it.
func (m *Model) viewReviewerImpact() string {
	if len(m.reviewer.impacts) == 0 {
		return ""
	}
	out := warn(fmt.Sprintf("  this change lands in %d measurement(s) already collecting:\n",
		len(m.reviewer.impacts)))
	for _, im := range m.reviewer.impacts {
		out += fmt.Sprintf("    %-6s %-28s %s\n", im.Grade, im.Class,
			dim(settings.ImpactNote(im)))
	}
	return out
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

// armingLabel names a registration the way its harness and scope read.
func armingLabel(a status.ScopeArming) string {
	if a.Harness == "" {
		return a.Scope + " scope"
	}
	return a.Harness + " " + a.Scope + " scope"
}
