package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// The Fixes panel is the inventory of what Steer installs to keep a problem from
// coming back: guards, rules, review addendum entries and this repository's
// checks, then the built-in guards you have not turned on. A built-in guard
// already in your inventory appears once, in yours. Each tier's row says where
// that fix stands in its own terms, and each action sits on the row whose scope it
// acts on.

// The kinds of row.
const (
	guardInstalled = "installed"
	guardAvailable = "available"
	guardRepair    = "repair"
	fixRule        = "rule"
	fixAddendum    = "addendum"
	fixRepo        = "repository"
	fixCheck       = "check"
	fixArchived    = "archived"
)

// fixRow is one line of the panel: a guard in your inventory, a rule, a review
// addendum entry, this repository or one of its checks, a built-in guard you can
// turn on, an archived fix, or a maintenance action a detected issue asks for.
type fixRow struct {
	kind string
	name string
	// origin is where an installed guard came from; description is a built-in
	// guard's own summary, a rule's or entry's text, or a check's binding.
	origin      string
	description string
	// source is an installed guard's stored source, and builtin marks one the
	// catalog can turn off and on again.
	source  string
	builtin bool
	// state is where a rule, entry, repository or check stands, and attention
	// marks a state that asks the reader to act.
	state     string
	attention bool
	// repair is the action a repair row runs and issue is what it fixes.
	repair string
	issue  string
	// scope is the store a fix lives in, and removable marks one x archives.
	scope     string
	removable bool
	// archived is the entry an archived row restores.
	archived *archivedEntry
}

// The repair actions. Each names one operation and one detected issue.
const (
	repairPresets = "update-built-ins"
	repairRunner  = "rebuild-runner"
	repairSync    = "sync-registrations"
)

// fixRows lists the rows in the order the panel prints them: your guards, rules,
// addendum entries, this repository and its checks, the built-in guards not in
// your inventory, the archived fixes, then one row per detected issue.
func (m *Model) fixRows() []fixRow {
	if m.snap == nil {
		return nil
	}
	var rows []fixRow
	visible := map[string]bool{}
	for _, p := range m.snap.Presets {
		visible[p.Name] = true
	}
	for _, h := range m.snap.Hooks {
		rows = append(rows, fixRow{kind: guardInstalled, name: h.Name,
			origin: guardOrigin(h.Source), source: h.Source,
			builtin: h.Source == claude.SourceFactory && visible[h.Name],
			scope:   m.scope, removable: h.Source != claude.SourceFactory})
	}
	rows = append(rows, m.ruleRows()...)
	rows = append(rows, m.addendumRows()...)
	rows = append(rows, m.checkRows()...)
	for _, p := range m.snap.Presets {
		if p.Enabled {
			// It is already in your inventory above. A second line here reads as two
			// protections where one is installed.
			continue
		}
		rows = append(rows, fixRow{kind: guardAvailable, name: p.Name,
			description: p.Description})
	}
	rows = append(rows, m.archivedRows()...)
	return append(rows, m.guardRepairs()...)
}

// ruleRows is one row per rule in either store, with the agents whose instruction
// file carries it and the agents whose file lacks it.
func (m *Model) ruleRows() []fixRow {
	var rows []fixRow
	for _, r := range m.snap.Rules {
		state, attention := m.ruleState(r)
		rows = append(rows, fixRow{kind: fixRule, name: r.ID, description: r.Text,
			state: r.Scope + " store; " + state, attention: attention,
			scope: r.Scope, removable: true})
	}
	return rows
}

// ruleState reads one rule against each installed agent's rules section in the
// rule's scope. A section that could not be read says nothing about the rule.
func (m *Model) ruleState(r status.RuleInfo) (string, bool) {
	var reading, missing []string
	for _, p := range m.snap.Posture.Rules {
		if p.Scope != r.Scope || p.State == status.RulesUnknown {
			continue
		}
		agent := status.HarnessLabel(p.Harness)
		if slices.Contains(p.Missing, r.ID) {
			missing = append(missing, agent)
			continue
		}
		reading = append(reading, agent)
	}
	var parts []string
	if len(reading) > 0 {
		parts = append(parts, "read by "+strings.Join(reading, " and "))
	}
	if len(missing) > 0 {
		parts = append(parts, "missing from the instructions of "+strings.Join(missing, " and ")+
			" — run: "+status.RulesRepairCommand(r.Scope))
	}
	if len(parts) == 0 {
		return "no installed agent's instructions were read for this store", false
	}
	return strings.Join(parts, "; "), len(missing) > 0
}

// addendumRows is one row per review addendum entry, read where the commit gate
// reads it. A project entry removed on disk but still committed is listed, because
// the gate still reads it; only an entry on disk can be archived.
func (m *Model) addendumRows() []fixRow {
	var rows []fixRow
	for _, a := range m.snap.Addenda {
		name, text := a.ID, a.Text
		if name == "" {
			name = "the " + a.Scope + " store"
		}
		if text == "" {
			text = a.HeadText
		}
		rows = append(rows, fixRow{kind: fixAddendum, name: name, description: text,
			state:     a.Scope + " store; " + status.AddendumPhrase(a),
			attention: a.State != status.AddendumLive,
			scope:     a.Scope, removable: a.ID != "" && a.Text != ""})
	}
	return rows
}

// checkRows are this repository, whose row authorizes the whole repository, and
// one row per declared check, whose row replays that one declaration. Outside a
// repository there is nothing to list.
func (m *Model) checkRows() []fixRow {
	c := m.snap.Checks
	if c.Repo == "" {
		return nil
	}
	rows := []fixRow{{kind: fixRepo, name: "this repository", description: c.Repo,
		state: status.CheckAdoptionPhrase(c), attention: c.Manifest && !c.Adopted}}
	for _, row := range c.Checks {
		rows = append(rows, fixRow{kind: fixCheck, name: row.ID, description: row.Binding,
			state:     status.CheckStatePhrase(row),
			attention: row.Validation == status.ValidationNone,
			scope:     claude.ProjectScope, removable: true})
	}
	return rows
}

// guardRepairs is one row per detected issue: a built-in guard whose installed
// copy no longer matches the catalog, and a guard runner older than this binary.
// A healthy screen carries neither, so nothing asks for routine maintenance.
func (m *Model) guardRepairs() []fixRow {
	var rows []fixRow
	var drifted []string
	for _, p := range m.snap.Presets {
		if p.Enabled && p.Drifted {
			drifted = append(drifted, p.Name)
		}
	}
	if len(drifted) > 0 {
		rows = append(rows, fixRow{
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
		rows = append(rows, fixRow{
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
	// A sync registers the existing runner and rewrites every rules section, so one
	// row serves both issues. It acts on the dashboard's scope, so the other scope
	// gets no row.
	for _, master := range m.snap.Posture.Masters {
		if master.Scope != m.scope || !master.Detached() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		rows = append(rows, fixRow{
			kind: guardRepair, repair: repairSync,
			name: "Run the guards in " + agent,
			issue: "The guard store in " + scopeReach(master.Scope) + " holds guards, and " + agent +
				" registers no runner for them, so none of them runs there. Syncing registers " +
				"the runner the store already holds and writes the rules sections. It leaves " +
				"the runner and your guard files as they are.",
		})
		break
	}
	for _, r := range m.snap.Posture.Rules {
		if r.Scope != m.scope || r.State != status.RulesDrifted ||
			hasRepair(rows, repairRunner) || hasRepair(rows, repairSync) {
			continue
		}
		agent := status.HarnessLabel(r.Harness)
		rows = append(rows, fixRow{
			kind: guardRepair, repair: repairSync,
			name: "Restore the rules " + agent + " reads",
			issue: "The instruction file " + agent + " reads in " + scopeReach(r.Scope) +
				" does not match the rules Steer holds: " + status.RulesPhrase(r) + ". " +
				"The agent reads only the rules in that file. " +
				"Syncing rewrites that section from Steer's rules. Lines you wrote inside " +
				"the section are removed; the rest of the file is left alone.",
		})
		break
	}
	return rows
}

// hasRepair reports whether a repair row for this action is already listed.
func hasRepair(rows []fixRow, repair string) bool {
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

// fixesLegend names the keys this panel answers, which depend on the row.
func (m *Model) fixesLegend() string {
	rows := m.fixRows()
	if len(rows) == 0 {
		return ""
	}
	archive := ""
	row := rows[m.cursor[panelFixes]]
	if row.removable && (row.kind != fixCheck || m.checking == "") {
		archive = "x archive · "
	}
	switch row.kind {
	case guardRepair:
		return "enter run this repair · "
	case guardInstalled:
		if row.builtin {
			return "enter turn off · "
		}
	case guardAvailable:
		return "enter turn on · "
	case fixRepo:
		if m.checking == "" && !m.snap.Checks.Adopted {
			return "enter authorize the whole repository (runs check.sh) · "
		}
	case fixCheck:
		if m.checking == "" {
			return "enter replay this one check · " + archive
		}
	case fixArchived:
		if row.archived.stored.Tier != string(tier.Check) || m.checking == "" {
			return "enter restore · "
		}
	}
	return archive
}

// fixesKey acts on the selected row. Enter turns a built-in guard on or off, runs
// a repair row's operation, authorizes the repository, replays a check or
// restores an archived fix; x opens the archive form over any other fix.
func (m *Model) fixesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.fixRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.cursor[panelFixes]]
	switch msg.String() {
	case "x":
		m.archiveKey(row)
		return m, nil
	case "enter":
	default:
		return m, nil
	}
	switch row.kind {
	case guardRepair:
		switch row.repair {
		case repairPresets:
			m.syncPresets()
		case repairSync:
			m.syncStore()
		default:
			m.rebuildMaster()
		}
	case guardAvailable:
		m.togglePreset(row.name, false)
	case guardInstalled:
		if row.builtin {
			m.togglePreset(row.name, true)
		} else if row.source == claude.SourceFactory {
			m.note("%s is a guard Steer keeps for itself, so it has no switch here", row.name)
		}
	case fixRepo:
		if m.checking != "" || m.snap.Checks.Adopted {
			return m, nil
		}
		return m, m.startCheckAction(checkAdopt, "")
	case fixCheck:
		if m.checking != "" {
			return m, nil
		}
		return m, m.startCheckAction(checkTest, row.name)
	case fixArchived:
		return m, m.restoreArchived(row.archived)
	}
	return m, nil
}

// The check actions. Each runs as a command: adoption replays every check without
// a current proof and then runs check.sh, and a replay or a restore builds and
// runs the check's cases, so any of them can take minutes.
const (
	checkAdopt   = "adopt"
	checkTest    = "test"
	checkRestore = "restore"
)

// checkActionMsg carries a finished check action's outcome.
type checkActionMsg struct {
	action string
	id     string
	note   string
}

// startCheckAction marks a check action in flight and returns the command that
// runs it, through the same checks functions `steer check adopt` and `steer check
// test` call. The repository is the one the dashboard reads.
func (m *Model) startCheckAction(action, id string) tea.Cmd {
	root := m.snap.Checks.Repo
	m.checking = action
	if action == checkAdopt {
		m.note("authorizing %s: replaying checks without a current proof, then running check.sh…", root)
	} else {
		m.note("replaying the cases of %s…", id)
	}
	return func() tea.Msg {
		if action == checkAdopt {
			res, err := checks.Adopt(root, checks.AdoptOptions{
				Stdout: io.Discard, Stderr: io.Discard, SteerBin: checks.SelfSteerBin(),
			})
			return checkActionMsg{action: action, note: adoptNote(root, res, err)}
		}
		run, err := checks.Test(root, id)
		return checkActionMsg{action: action, id: id, note: testNote(id, run, err)}
	}
}

// checkActionDone recomposes over what the action recorded and files its note
// under this panel, wherever the reader is now.
func (m *Model) checkActionDone(msg checkActionMsg) {
	m.checking = ""
	m.refresh()
	m.noteFor(panelFixes, "%s", msg.note)
}

// adoptNote says what an adoption attempt did, in one line.
func adoptNote(root string, res checks.AdoptResult, err error) string {
	switch {
	case res.Already:
		return root + " was already authorized on this machine"
	case err != nil:
		return "not authorized: " + err.Error()
	}
	return "authorized " + root + " on this machine; check.sh runs at turn end only where the check runner is registered"
}

// testNote says what a replay found, in one line.
func testNote(id string, run checks.ValidationRun, err error) string {
	switch {
	case run.Verdict == checks.VerdictValidated:
		return id + " replayed: it passed the known-good case and failed the defect case"
	case run.Verdict != "" && run.Reason != "":
		return id + " " + run.Verdict + ": " + run.Reason
	case run.Verdict != "":
		return id + " " + run.Verdict
	case err != nil:
		return "error: " + err.Error()
	}
	return id + " replayed"
}

// togglePreset turns a built-in guard on or off through the same presets
// functions the CLI commands call. A guard the catalog does not hold is not a
// built-in guard, so the panel names the archive action instead.
func (m *Model) togglePreset(name string, on bool) {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		m.note("could not read the built-in guards: %v", err)
		return
	}
	item, found := presets.FindVisiblePreset(catalog, name)
	if !found {
		m.note("%s is not a built-in guard in this Steer; x archives it instead", name)
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

// syncStore registers the store's existing runner and rules in every target,
// through the same package function `steer hooks sync` calls.
func (m *Model) syncStore() {
	res, err := claude.SyncStore(m.scope, m.targets...)
	if err != nil {
		m.note("nothing was synced: %v", err)
		return
	}
	m.refresh()
	m.note("synced %d agent(s); %d already matched the store", len(res.Synced), len(res.Current))
}

// viewFixes renders each tier's fixes, the built-in guards you can turn on, and
// any repair a detected issue asks for.
func (m *Model) viewFixes() string {
	if m.editing && m.archiveForm != nil {
		return m.viewArchiveForm()
	}
	width := m.bodyWidth()
	out := m.title("Fixes — what Steer installed to keep a problem from coming back")
	rows := m.fixRows()
	column := guardNameWidth(rows, width)

	out += section("Guards — run before your coding agent acts")
	if !anyOfKind(rows, guardInstalled) {
		out += wrapTo(m.emptyGuardNote(), "  ", width)
	}
	for i, r := range rows {
		if r.kind != guardInstalled {
			continue
		}
		out += guardTitleInColumn(r.name, r.origin, i == m.cursor[panelFixes], width, guardNameWidth(rows, width))
	}
	if anyOfKind(rows, guardInstalled) {
		out += wrapTo(secondary("Turning a built-in guard off writes no archive: it comes back from the built-in "+
			"guards below. Archiving any other guard keeps its file and fixtures for a restore."), "  ", width)
	}
	out += wrapTo(secondary("This is what Steer registered here. It is not every hook your coding "+
		"agent runs: review before commit, and any hook you set up yourself, are not listed."),
		"  ", width)
	out += wrapTo(dim("Stored in "+m.guardStorePath()), "  ", width)

	out += section("Rules — instructions your coding agent reads")
	if !anyOfKind(rows, fixRule) {
		out += wrapTo(dim("no rules in either store."), "  ", width)
	}
	out += stateRows(rows, fixRule, m.cursor[panelFixes], width, column)

	out += section("Review addendum — what the commit gate's reviewer is told")
	if !anyOfKind(rows, fixAddendum) {
		out += wrapTo(dim("no entries in either store."), "  ", width)
	}
	out += stateRows(rows, fixAddendum, m.cursor[panelFixes], width, column)

	out += section("Checks — run by check.sh at the end of a turn")
	if !anyOfKind(rows, fixRepo) {
		out += wrapTo(dim("not inside a repository, so there are no checks to read."), "  ", width)
	}
	out += stateRows(rows, fixRepo, m.cursor[panelFixes], width, column)
	out += stateRows(rows, fixCheck, m.cursor[panelFixes], width, column)
	if m.checking != "" {
		out += wrapTo(secondary("A check action is running; its result appears at the bottom of the screen when it finishes."), "  ", width)
	}

	out += section("Available built-in guards")
	if !anyOfKind(rows, guardAvailable) {
		out += dim("  every built-in guard is already on here.\n")
	}
	for i, r := range rows {
		if r.kind != guardAvailable {
			continue
		}
		out += guardTitleInColumn(r.name, r.description, i == m.cursor[panelFixes], width, guardNameWidth(rows, width))
	}

	out += section("Archived — fixes a removal took out, which enter puts back")
	if !anyOfKind(rows, fixArchived) && len(m.archiveUnread) == 0 {
		out += dim("  nothing archived in the project or global stores, or in this repository's checks.\n")
	}
	for _, line := range m.archiveUnread {
		out += wrapTo(warn(line), "  ", width)
	}
	out += stateRows(rows, fixArchived, m.cursor[panelFixes], width, column)

	if !anyOfKind(rows, guardRepair) {
		return out
	}
	out += section("Maintenance needed")
	for i, r := range rows {
		if r.kind != guardRepair {
			continue
		}
		out += selectedRow(r.name, " "+warn("needs attention"), i == m.cursor[panelFixes], width)
		out += wrapTo(r.issue, "    ", width)
	}
	return out
}

// stateRows renders the rows of one kind: the name with its text or binding, and
// under it where that fix stands, marked when it asks the reader to act.
func stateRows(rows []fixRow, kind string, cursor, width, column int) string {
	out := ""
	for i, r := range rows {
		if r.kind != kind {
			continue
		}
		out += guardTitleInColumn(r.name, r.description, i == cursor, width, column)
		state := secondary(r.state)
		if r.attention {
			state = warn(r.state)
		}
		out += wrapTo(state, "    ", width)
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
func anyOfKind(rows []fixRow, kind string) bool {
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

func guardNameWidth(rows []fixRow, width int) int {
	column := 0
	for _, row := range rows {
		if row.kind != guardRepair {
			column = max(column, lipgloss.Width(row.name))
		}
	}
	return min(column, width/2)
}
