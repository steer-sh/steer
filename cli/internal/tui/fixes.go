package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// The Fixes panel is the inventory of what Steer installs to keep a problem from
// coming back: guards, rules, review prompt entries and this repository's
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
	// A disclosure row opens or closes the section under it.
	catalogToggle = "catalog-toggle"
	archiveToggle = "archive-toggle"
)

// fixRow is one line of the panel: a guard in your inventory, a rule, a review
// addendum entry, this repository or one of its checks, a built-in guard you can
// turn on, an archived fix, or a maintenance action a detected issue asks for.
type fixRow struct {
	kind string
	// name is the fix's id, the one archive and restore act on; label is what the
	// row shows when it differs, and a row that names no fix carries a label only.
	name  string
	label string
	// compact shows the first line of description only.
	compact bool
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
	// unremovable is why x cannot archive a fix of a tier it otherwise archives.
	unremovable string
	// archived is the entry an archived row restores.
	archived *archivedEntry
}

// display is the name the row shows.
func (r fixRow) display() string {
	if r.label != "" {
		return r.label
	}
	return r.name
}

// fixKey identifies a rule or review prompt entry across a recompose.
func fixKey(r fixRow) string {
	return r.kind + "\x00" + r.scope + "\x00" + r.name
}

// The repair actions. Each names one operation and one detected issue.
const (
	repairPresets = "update-built-ins"
	repairRunner  = "rebuild-runner"
	repairSync    = "sync-registrations"
)

// fixRows lists the rows in the order the panel prints them, which is the order
// the cursor walks: one row per detected issue, your guards, rules, review prompt
// entries, this repository and its checks, then the built-in guards not in your
// inventory and the archived fixes, each behind a row that opens it.
func (m *Model) fixRows() []fixRow {
	if m.snap == nil {
		return nil
	}
	rows := m.guardRepairs()
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
	var catalog []fixRow
	for _, p := range m.snap.Presets {
		if p.Enabled {
			// It is already in your inventory above. A second line here reads as two
			// protections where one is installed.
			continue
		}
		catalog = append(catalog, fixRow{kind: guardAvailable, name: p.Name,
			description: p.Description})
	}
	rows = append(rows, disclose(catalogToggle, catalog, m.showCatalog,
		fmt.Sprintf("%d you can turn on", len(catalog)))...)
	archived := m.archivedRows()
	return append(rows, disclose(archiveToggle, archived, m.showArchived,
		fmt.Sprintf("%d archived %s", len(archived), fixNoun(len(archived))))...)
}

// fixNoun is "fix" or "fixes".
func fixNoun(n int) string {
	if n == 1 {
		return "fix"
	}
	return "fixes"
}

// disclose is a section's opening row, then its rows while it is open. An empty
// section has no opening row: its empty state is printed instead.
func disclose(kind string, rows []fixRow, open bool, count string) []fixRow {
	if len(rows) == 0 {
		return nil
	}
	verb := "enter shows them"
	if open {
		verb = "enter hides them"
	}
	out := []fixRow{{kind: kind, label: count, description: verb}}
	if open {
		out = append(out, rows...)
	}
	return out
}

// ruleRows is one row per rule in either store, with the agents whose instruction
// file carries it and the agents whose file lacks it.
func (m *Model) ruleRows() []fixRow {
	var rows []fixRow
	for _, r := range m.snap.Rules {
		state, attention := m.ruleState(r)
		row := fixRow{kind: fixRule, name: r.ID, description: r.Text,
			state: r.Scope + "; " + state, attention: attention,
			scope: r.Scope, removable: true}
		row.compact = fixKey(row) != m.fixOpen
		rows = append(rows, row)
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
		return "no installed agent's instructions were read at this scope", false
	}
	return strings.Join(parts, "; "), len(missing) > 0
}

// addendumRows is one row per review addendum entry, read where the commit gate
// reads it. A project entry removed on disk but still committed is listed, because
// the gate still reads it; only an entry on disk can be archived.
func (m *Model) addendumRows() []fixRow {
	var rows []fixRow
	for _, a := range m.snap.Addenda {
		text := a.Text
		if text == "" {
			text = a.HeadText
		}
		row := fixRow{kind: fixAddendum, name: a.ID, description: text,
			state:       a.Scope + "; " + status.AddendumPhrase(a),
			attention:   a.State != status.AddendumLive,
			scope:       a.Scope,
			removable:   a.ID != "" && a.Text != "",
			unremovable: addendumUnremovable(a)}
		if a.ID == "" {
			row.label = "the " + a.Scope + " review prompt"
		}
		row.compact = fixKey(row) != m.fixOpen
		rows = append(rows, row)
	}
	return rows
}

// addendumUnremovable is why x cannot archive one addendum row, "" for an entry it
// can. An entry the working-tree store no longer holds has nothing left to archive,
// and the gate stops reading it when the removal is committed. A row that names no
// entry stands for a store the reader could not read.
func addendumUnremovable(a status.AddendumInfo) string {
	switch {
	case a.ID == "":
		return "this row stands for the whole " + a.Scope + " review prompt, not one entry, so there is nothing to archive"
	case a.Text == "":
		return a.ID + " is no longer in the " + a.Scope + " review prompt on disk, so there is nothing left to archive; " +
			commitStep("removal")
	}
	return ""
}

// checkRows are this repository, whose row authorizes the whole repository and
// names its last check.sh run, and one row per declared check, whose row tests
// that one declaration. Outside a
// repository there is nothing to list.
func (m *Model) checkRows() []fixRow {
	c := m.snap.Checks
	if c.Repo == "" {
		return nil
	}
	rows := []fixRow{{kind: fixRepo, label: "this repository", description: c.Repo,
		state:     status.CheckAdoptionPhrase(c) + "; " + lastRunPhrase(c.LatestRun),
		attention: c.Manifest && !c.Adopted}}
	for _, row := range c.Checks {
		rows = append(rows, fixRow{kind: fixCheck, name: row.ID, description: row.Binding,
			state:     status.CheckStatePhrase(row),
			attention: row.Validation == status.ValidationNone,
			scope:     claude.ProjectScope, removable: true})
	}
	return rows
}

// lastRunPhrase names the repository's last check.sh run that did any work.
func lastRunPhrase(r *status.CheckRun) string {
	if r == nil {
		return status.CheckRunPhrase(nil)
	}
	return "last run " + status.CheckRunPhrase(r)
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
			label: "Update built-in guards",
			issue: fmt.Sprintf("%s no longer %s the built-in guard this version of Steer ships. "+
				"Updating reinstalls %s from the catalog in the running Steer, and puts back any "+
				"supporting guard Steer keeps for itself. Local edits to %s are replaced.",
				strings.Join(drifted, ", "), matchVerb(len(drifted)),
				pronoun(len(drifted)), pronoun(len(drifted))),
		})
	}
	// A rebuild writes the runner in the dashboard's scope, so a stale runner in
	// the other scope gets no row here; Diagnostics carries its command instead.
	for _, master := range m.snap.Posture.Masters {
		if master.Scope != m.scope || !master.Stale() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		rows = append(rows, fixRow{
			kind: guardRepair, repair: repairRunner,
			label: "Update the guard runner for " + agent,
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
			label: "Run the guards in " + agent,
			issue: "Guard files are installed in " + scopeReach(master.Scope) + ", and " + agent +
				" registers no runner for them, so none of them runs there. Syncing registers " +
				"the runner already built from those files and writes the rules sections. It leaves " +
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
			label: "Restore the rules " + agent + " reads",
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
	case catalogToggle, archiveToggle:
		return "enter " + strings.TrimPrefix(row.description, "enter ") + " · "
	case fixRule, fixAddendum:
		if row.compact {
			return "enter show full text · " + archive
		}
		return "enter hide full text · " + archive
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
			return "enter test this one check · " + archive
		}
	case fixArchived:
		if row.archived.stored.Tier != string(tier.Check) || m.checking == "" {
			return "enter restore · "
		}
	}
	return archive
}

// fixesKey acts on the selected row. Enter turns a built-in guard on or off, runs
// a repair row's operation, authorizes the repository, tests a check, restores an
// archived fix, shows or hides an entry's full text, or opens a section; x opens
// the archive form over any other fix.
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
	case catalogToggle:
		m.showCatalog = !m.showCatalog
		m.clampCursor(panelFixes)
	case archiveToggle:
		m.showArchived = !m.showArchived
		m.clampCursor(panelFixes)
	case fixRule, fixAddendum:
		if row.compact {
			m.fixOpen = fixKey(row)
		} else {
			m.fixOpen = ""
		}
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
		m.note("authorizing %s: testing checks without a current proof, then running check.sh…", root)
	} else {
		m.note("testing the cases of %s…", id)
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

// The adopt note's bounds. The note shares the frame with the panel it reports
// to, so each part names a count rather than growing with the repository.
const (
	adoptNoteRows    = 3
	adoptNoteExcerpt = 3
)

// adoptNote says what an adoption attempt did. The result leads, then the command
// that prints the whole run, then the facts the attempt already returned: the
// oracle's own ending, the checks that did not pass, and an excerpt where nothing
// else explains the failure.
func adoptNote(root string, res checks.AdoptResult, err error) string {
	if res.Already {
		return root + " was already authorized on this machine"
	}
	if err == nil {
		return "authorized " + root + " on this machine; check.sh runs at turn end only where the check runner is registered"
	}
	lines := []string{
		"not authorized: " + err.Error(),
		"steer check adopt " + root + " runs the adoption again and prints the whole run",
	}
	lines = append(lines, oracleOutcomeLine(res.Oracle)...)
	lines = append(lines, adoptFailureRows(res)...)
	if res.Oracle.IntegrationFailure && len(res.Oracle.Missing) > 0 {
		lines = append(lines, "Add "+checks.ExecInvocation+" to its sequence: steer check guide")
	}
	return strings.Join(append(lines, adoptExcerptLines(res)...), "\n")
}

// oracleOutcomeLine reports check.sh's own ending where the refusal does not
// already carry it. Adopt's error names the outcome, so the line earns its place
// through a reason or an exit code.
func oracleOutcomeLine(run checks.OracleRun) []string {
	switch {
	case run.RunID == "":
		return nil
	case run.Reason != "":
		return []string{checks.OracleName + " " + run.Outcome + ": " + run.Reason}
	case run.ExitCode != nil && *run.ExitCode != 0:
		return []string{fmt.Sprintf("%s %s with exit %d", checks.OracleName, run.Outcome, *run.ExitCode)}
	}
	return nil
}

// adoptFailureRows names what did not pass, bounded and worst first. Cases that do
// not validate stop the adoption before check.sh runs; a check the oracle never
// reached explains nothing about the failure, so it sorts last.
func adoptFailureRows(res checks.AdoptResult) []string {
	var rows, unreached []string
	for _, v := range res.Validations {
		if !v.Reused && v.Run.Verdict != checks.VerdictValidated {
			rows = append(rows, withReason(v.Run.CheckID+" "+v.Run.Verdict, v.Run.Reason))
		}
	}
	for _, e := range res.Executions {
		if e.Passed() {
			continue
		}
		row := withReason(e.CheckID+" "+e.Outcome, e.Reason)
		if e.Outcome == checks.OutcomeNotExecuted {
			unreached = append(unreached, row)
			continue
		}
		rows = append(rows, row)
	}
	rows = append(rows, unreached...)
	if len(rows) > adoptNoteRows {
		return append(rows[:adoptNoteRows], fmt.Sprintf("and %d more", len(rows)-adoptNoteRows))
	}
	return rows
}

// withReason joins a row to its reason, and leaves a row without one alone.
func withReason(head, reason string) string {
	if reason == "" {
		return head
	}
	return head + ": " + reason
}

// adoptExcerptLines are the last lines check.sh printed, for a failure no
// structured reason explains: an ordinary non-zero exit leaves the run without a
// reason, and a check that simply failed leaves its row without one. A check the
// oracle never reached carries a reason about the oracle, not about the failure, so
// it does not answer for one.
func adoptExcerptLines(res checks.AdoptResult) []string {
	if res.Oracle.Reason != "" || strings.TrimSpace(res.Oracle.Excerpt) == "" {
		return nil
	}
	for _, e := range res.Executions {
		if !e.Passed() && e.Outcome != checks.OutcomeNotExecuted && e.Reason != "" {
			return nil
		}
	}
	var out []string
	for _, line := range lastNonEmptyLines(res.Oracle.Excerpt, adoptNoteExcerpt) {
		out = append(out, "| "+line)
	}
	return out
}

// lastNonEmptyLines is the tail of s, up to n lines, without its blank lines.
func lastNonEmptyLines(s string, n int) []string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimRight(line, " \t"))
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

// testNote says what a replay found, in one line.
func testNote(id string, run checks.ValidationRun, err error) string {
	switch {
	case run.Verdict == checks.VerdictValidated:
		return id + " tested: it passed the known-good case and failed the defect case"
	case run.Verdict != "" && run.Reason != "":
		return id + " " + run.Verdict + ": " + run.Reason
	case run.Verdict != "":
		return id + " " + run.Verdict
	case err != nil:
		return "error: " + err.Error()
	}
	return id + " tested"
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
	var removed []presets.CatalogItem
	if on {
		_, removed, err = presets.DisablePreset(m.scope, catalog, item, false, m.targets...)
	} else {
		_, err = presets.EnablePreset(m.scope, catalog, item, false, m.targets...)
		if err == nil {
			_, err = presets.EnsureAutoManagedFactoryPresets(m.scope, catalog, false, m.targets...)
		}
	}
	// A disable can fail after it removed the guard, so the panel reads the store
	// before it reports the error.
	m.refresh()
	if err != nil {
		m.note("error: %v", err)
		return
	}
	if on {
		if len(removed) > 0 {
			m.note("%s is off in %s; %s went with it, as no built-in guard remains", name, scopeReach(m.scope), removed[0].Name)
			return
		}
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
	m.note("synced %d agent(s); %d already matched the guard files", len(res.Synced), len(res.Current))
}

// viewFixes renders any repair a detected issue asks for, each tier's fixes, and
// the built-in guards you can turn on and the archived fixes behind a row each.
func (m *Model) viewFixes() string {
	if m.editing && m.archiveForm != nil {
		return m.viewArchiveForm()
	}
	width := m.bodyWidth()
	out := m.title("Fixes — what Steer installed to keep a problem from coming back")
	rows := m.fixRows()
	cursor := m.cursor[panelFixes]

	if anyOfKind(rows, guardRepair) {
		out += section("Maintenance needed")
		for i, r := range rows {
			if r.kind != guardRepair {
				continue
			}
			out += selectedRow(r.display(), " "+warn("needs attention"), i == cursor, width)
			out += wrapTo(r.issue, "    ", width)
		}
	}

	out += section("Guards Steer registered — run before your coding agent acts")
	if !anyOfKind(rows, guardInstalled) {
		out += wrapTo(m.emptyGuardNote(), "  ", width)
	}
	column := nameColumn(rows, width, guardInstalled)
	for i, r := range rows {
		if r.kind == guardInstalled {
			out += guardTitleInColumn(r.display(), r.origin, i == cursor, width, column)
		}
	}
	if anyOfKind(rows, guardInstalled) {
		out += wrapTo(secondary("Turning a built-in guard off writes no archive: it comes back from the built-in "+
			"guards below. Archiving any other guard keeps its file and fixtures for a restore."), "  ", width)
	}

	out += section("Rules — instructions your coding agent reads")
	if !anyOfKind(rows, fixRule) {
		out += wrapTo(dim("No rules installed, project or global."), "  ", width)
	}
	out += stateRows(rows, cursor, width, fixRule)

	out += section("Review prompt entries — what the commit gate's reviewer is told")
	if !anyOfKind(rows, fixAddendum) {
		out += wrapTo(dim("No entries installed, project or global."), "  ", width)
	}
	out += stateRows(rows, cursor, width, fixAddendum)

	out += section("Checks — run by check.sh at the end of a turn")
	if !anyOfKind(rows, fixRepo) {
		out += wrapTo(dim("not inside a repository, so there are no checks to read."), "  ", width)
	}
	out += stateRows(rows, cursor, width, fixRepo, fixCheck)
	if m.checking != "" {
		out += wrapTo(secondary("A check action is running; its result appears at the bottom of the screen when it finishes."), "  ", width)
	}

	out += section("Available built-in guards")
	if !anyOfKind(rows, catalogToggle) {
		out += dim("  every built-in guard is already on here.\n")
	}
	column = nameColumn(rows, width, guardAvailable)
	for i, r := range rows {
		switch r.kind {
		case catalogToggle:
			out += selectedRow(r.display(), " "+secondary("— "+r.description), i == cursor, width)
		case guardAvailable:
			out += guardTitleInColumn(r.display(), r.description, i == cursor, width, column)
		}
	}

	out += section("Archived — fixes a removal took out, which enter puts back")
	if !anyOfKind(rows, archiveToggle) && len(m.archiveUnread) == 0 {
		out += dim("  Nothing archived, project or global, or in this repository's checks.\n")
	}
	for _, line := range m.archiveUnread {
		out += wrapTo(warn(line), "  ", width)
	}
	for i, r := range rows {
		if r.kind == archiveToggle {
			out += selectedRow(r.display(), " "+secondary("— "+r.description), i == cursor, width)
		}
	}
	return out + stateRows(rows, cursor, width, fixArchived)
}

// stateRows renders the rows of the named kinds: the name with its text or
// binding, and under it where that fix stands, marked when it asks the reader to
// act. The name column is the section's own, so a long name elsewhere on the
// panel never narrows this text. A compact row shows its text's first line.
func stateRows(rows []fixRow, cursor, width int, kinds ...string) string {
	column := nameColumn(rows, width, kinds...)
	out := ""
	for i, r := range rows {
		if !slices.Contains(kinds, r.kind) {
			continue
		}
		description := r.description
		if r.compact {
			description = ansi.Truncate(strings.Join(strings.Fields(description), " "),
				max(1, width-column-6), "…")
		}
		out += guardTitleInColumn(r.display(), description, i == cursor, width, column)
		state := secondary(r.state)
		if r.attention {
			state = warn(r.state)
		}
		out += wrapTo(state, "    ", width)
	}
	return out
}

// emptyGuardNote says an empty inventory is empty in this scope, and names the
// scope, so it claims nothing about the other one or another machine.
func (m *Model) emptyGuardNote() string {
	return secondary("No Steer guards registered for " + scopeReach(m.scope) + ".")
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

// nameColumn is the width the names of one section share, capped at half the
// panel.
func nameColumn(rows []fixRow, width int, kinds ...string) int {
	column := 0
	for _, row := range rows {
		if slices.Contains(kinds, row.kind) {
			column = max(column, lipgloss.Width(row.display()))
		}
	}
	return min(column, width/2)
}
