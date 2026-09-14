package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/status"
)

// The Guards panel is the inventory of checks that run before your coding agent
// acts. Your guards come first, then the built-in ones you have not turned on. A
// built-in guard already in your inventory appears once, in yours. Each
// maintenance action sits beside the issue that asks for it.

// The kinds of row enter acts on.
const (
	guardInstalled = "installed"
	guardAvailable = "available"
	guardRepair    = "repair"
)

// guardRow is one line of the panel: a guard in your inventory, a built-in guard
// you can turn on, or a maintenance action a detected issue asks for.
type guardRow struct {
	kind string
	name string
	// origin is where an installed guard came from; description is a built-in
	// guard's own summary.
	origin      string
	description string
	// repair is the action a repair row runs and issue is what it fixes.
	repair string
	issue  string
}

// The repair actions. Each names one operation and one detected issue.
const (
	repairPresets = "update-built-ins"
	repairRunner  = "rebuild-runner"
)

// guardRows lists your guards, then the built-in guards not in that inventory,
// then one row per detected issue.
func (m *Model) guardRows() []guardRow {
	if m.snap == nil {
		return nil
	}
	var rows []guardRow
	for _, h := range m.snap.Hooks {
		rows = append(rows, guardRow{kind: guardInstalled, name: h.Name,
			origin: guardOrigin(h.Source)})
	}
	for _, p := range m.snap.Presets {
		if p.Enabled {
			// It is already in your inventory above. A second line here reads as two
			// protections where one is installed.
			continue
		}
		rows = append(rows, guardRow{kind: guardAvailable, name: p.Name,
			description: p.Description})
	}
	return append(rows, m.guardRepairs()...)
}

// guardRepairs is one row per detected issue: a built-in guard whose installed
// copy no longer matches the catalog, and a guard runner older than this binary.
// A healthy screen carries neither, so nothing asks for routine maintenance.
func (m *Model) guardRepairs() []guardRow {
	var rows []guardRow
	var drifted []string
	for _, p := range m.snap.Presets {
		if p.Enabled && p.Drifted {
			drifted = append(drifted, p.Name)
		}
	}
	if len(drifted) > 0 {
		rows = append(rows, guardRow{
			kind: guardRepair, repair: repairPresets,
			name: "Update built-in guards",
			issue: fmt.Sprintf("%s no longer %s the built-in guard this version of Steer ships. "+
				"Updating reinstalls %s from the catalog in the running Steer, and puts back any "+
				"supporting guard Steer keeps for itself. Local edits to %s are replaced.",
				strings.Join(drifted, ", "), matchVerb(len(drifted)),
				pronoun(len(drifted)), pronoun(len(drifted))),
		})
	}
	for _, master := range m.snap.Posture.Masters {
		if !master.Stale() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		rows = append(rows, guardRow{
			kind: guardRepair, repair: repairRunner,
			name: "Update the guard runner for " + agent,
			issue: "The shared runner registered for " + agent + " in " + scopeReach(master.Scope) +
				" is older than this Steer, or carries no version at all. Rebuilding it writes a " +
				"new runner from the guard files you already have and repoints " + agent +
				" at it. Your guard files are left alone; with no guards, the registration is " +
				"removed instead.",
		})
		break
	}
	// The rebuild also rewrites every rules section, so one row serves both issues.
	// It acts on the dashboard's scope, so a section in the other scope gets no row.
	for _, r := range m.snap.Posture.Rules {
		if r.Scope != m.scope || r.State != status.RulesDrifted || hasRepair(rows, repairRunner) {
			continue
		}
		agent := status.HarnessLabel(r.Harness)
		rows = append(rows, guardRow{
			kind: guardRepair, repair: repairRunner,
			name: "Restore the rules " + agent + " reads",
			issue: "The instruction file " + agent + " reads in " + scopeReach(r.Scope) +
				" does not match the rules Steer holds: " + status.RulesPhrase(r) + ". " +
				"The agent reads only the rules in that file. " +
				"Rebuilding rewrites that section from Steer's rules and writes the shared " +
				"runner again. Lines you wrote inside the section are removed; the rest of " +
				"the file is left alone.",
		})
		break
	}
	return rows
}

// hasRepair reports whether a repair row for this action is already listed.
func hasRepair(rows []guardRow, repair string) bool {
	for _, row := range rows {
		if row.kind == guardRepair && row.repair == repair {
			return true
		}
	}
	return false
}

// matchVerb and pronoun keep the issue sentence grammatical for one or many.
func matchVerb(n int) string {
	if n == 1 {
		return "matches"
	}
	return "match"
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// guardOrigin says where a guard in your inventory came from, in the reader's
// words. The stored source arrives as data, so it is translated here.
func guardOrigin(source string) string {
	switch source {
	case claude.SourceGenerated:
		return "written for you by Steer"
	case claude.SourceFactory:
		return "built in to Steer"
	case claude.SourceManual:
		return "written by you, registered with Steer"
	}
	return source
}

// guardsLegend names the keys this panel answers, which depend on the row.
func (m *Model) guardsLegend() string {
	rows := m.guardRows()
	if len(rows) == 0 {
		return ""
	}
	switch rows[m.cursor[panelGuards]].kind {
	case guardRepair:
		return "enter run this repair · "
	case guardInstalled:
		return "enter turn off · "
	default:
		return "enter turn on · "
	}
}

// guardsKey acts on the selected row: a built-in guard turns on or off, and a
// repair row runs the operation its issue names.
func (m *Model) guardsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "enter" {
		return m, nil
	}
	rows := m.guardRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.cursor[panelGuards]]
	switch row.kind {
	case guardRepair:
		if row.repair == repairPresets {
			m.syncPresets()
			return m, nil
		}
		m.rebuildMaster()
	case guardAvailable:
		m.togglePreset(row.name, false)
	case guardInstalled:
		m.togglePreset(row.name, true)
	}
	return m, nil
}

// togglePreset turns a built-in guard on or off through the same presets
// functions the CLI commands call. A guard the catalog does not hold is not a
// built-in guard, so the panel names where it can be removed instead.
func (m *Model) togglePreset(name string, on bool) {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		m.note("could not read the built-in guards: %v", err)
		return
	}
	item, found := presets.FindVisiblePreset(catalog, name)
	if !found {
		m.note("%s is not a built-in guard; remove it with: steer hooks remove %s", name, name)
		return
	}
	if on {
		_, err = presets.DisablePreset(m.scope, item, false, m.targets...)
	} else {
		_, err = presets.EnablePreset(m.scope, catalog, item, false, m.targets...)
		if err == nil {
			_, err = presets.EnsureAutoManagedFactoryPresets(m.scope, catalog, false, m.targets...)
		}
	}
	if err != nil {
		m.note("error: %v", err)
		return
	}
	m.refresh()
	if on {
		m.note("%s is off in %s", name, scopeReach(m.scope))
		return
	}
	m.note("%s is on in %s", name, scopeReach(m.scope))
}

// syncPresets reconciles the built-in guards you have on with the catalog in the
// running Steer.
func (m *Model) syncPresets() {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		m.note("could not read the built-in guards: %v", err)
		return
	}
	res, err := presets.SyncFactoryPresets(m.scope, catalog, presets.SyncOptions{}, m.targets...)
	if err != nil {
		m.note("nothing was updated: %v", err)
		return
	}
	m.refresh()
	m.note("%d built-in guard(s) read, %d replaced, %d already current",
		res.CheckedFactory, res.Reinstalled, res.UpToDate)
}

// rebuildMaster writes the shared runner again and repoints every registration at
// it, through the same package function `steer hooks rebuild` calls.
func (m *Model) rebuildMaster() {
	res, err := claude.RebuildMaster(m.scope, m.targets...)
	if err != nil {
		m.note("the runner was not rebuilt: %v", err)
		return
	}
	m.refresh()
	if res.Hooks == 0 {
		m.note("no guards in %s, so the runner registration was removed", scopeReach(m.scope))
		return
	}
	m.note("the runner now carries %d guard(s), version %s", res.Hooks, res.Stamp.Runtime)
}

// viewGuards renders your guards, the built-in guards you can turn on, and any
// repair a detected issue asks for.
func (m *Model) viewGuards() string {
	width := m.bodyWidth()
	out := m.title("Guards — checks that run before your coding agent acts")
	rows := m.guardRows()

	out += section("Your guards")
	if !anyOfKind(rows, guardInstalled) {
		out += wrapTo(m.emptyGuardNote(), "  ", width)
	}
	for i, r := range rows {
		if r.kind != guardInstalled {
			continue
		}
		out += guardTitleInColumn(r.name, r.origin, i == m.cursor[panelGuards], width, guardNameWidth(rows, width))
	}
	out += wrapTo(secondary("This is what Steer registered here. It is not every hook your coding "+
		"agent runs: review before commit, and any hook you set up yourself, are not listed."),
		"  ", width)
	out += wrapTo(dim("Stored in "+m.guardStorePath()), "  ", width)

	out += section("Available built-in guards")
	if !anyOfKind(rows, guardAvailable) {
		out += dim("  every built-in guard is already on here.\n")
	}
	for i, r := range rows {
		if r.kind != guardAvailable {
			continue
		}
		out += guardTitleInColumn(r.name, r.description, i == m.cursor[panelGuards], width, guardNameWidth(rows, width))
	}

	if !anyOfKind(rows, guardRepair) {
		return out
	}
	out += section("Maintenance needed")
	for i, r := range rows {
		if r.kind != guardRepair {
			continue
		}
		out += selectedRow(r.name, " "+warn("needs attention"), i == m.cursor[panelGuards], width)
		out += wrapTo(r.issue, "    ", width)
	}
	return out
}

// emptyGuardNote says an empty inventory is empty HERE. This scope's store says
// nothing about the other scope, about another machine, or about a guard a coding
// agent runs that Steer did not register.
func (m *Model) emptyGuardNote() string {
	return secondary("No guards recorded for " + scopeReach(m.scope) +
		". Guards in the other location, and on your other machines, are not read here.")
}

// guardStorePath is where this scope's guards live, so a reader can look. A store
// that cannot be resolved names itself rather than reporting nothing.
func (m *Model) guardStorePath() string {
	path, err := hookstore.Resolve(m.scope)
	if err != nil {
		return "an unreadable location: " + err.Error()
	}
	return path
}

// anyOfKind says whether the list holds a row of one kind, so a section can print
// its own empty state.
func anyOfKind(rows []guardRow, kind string) bool {
	for _, r := range rows {
		if r.kind == kind {
			return true
		}
	}
	return false
}

func guardTitle(name, description string, selected bool, width int) string {
	return guardTitleInColumn(name, description, selected, width, min(lipgloss.Width(name), width/2))
}

func guardTitleInColumn(name, description string, selected bool, width, column int) string {
	if width < 70 || lipgloss.Width(name) > column {
		return selectedRow(name, "", selected, width) + wrapTo(secondary(description), "    ", width)
	}
	indent := strings.Repeat(" ", column+3)
	lines := strings.Split(strings.TrimRight(wrapTo(secondary(description), indent, width), "\n"), "\n")
	out := selectedRow(name+strings.Repeat(" ", column-lipgloss.Width(name)), " "+strings.TrimPrefix(lines[0], indent), selected, width)
	for _, line := range lines[1:] {
		if selected {
			line = paintSurface(selectedStyle.Width(width), line)
		}
		out += line + "\n"
	}
	return out
}

func guardNameWidth(rows []guardRow, width int) int {
	column := 0
	for _, row := range rows {
		if row.kind != guardRepair {
			column = max(column, lipgloss.Width(row.name))
		}
	}
	return min(column, width/2)
}
