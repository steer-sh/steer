package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// archiveForm is the editor the Fixes panel opens over one fix before it archives
// it: the fix's tier, id and store scope, and the reason the entry keeps.
type archiveForm struct {
	tier  string
	id    string
	scope string
}

// archivedEntry is the newest archive entry of one id in one tier and store.
// older counts the entries of that id the store holds before it. root is the
// repository of a check entry, "" for the other tiers.
type archivedEntry struct {
	scope  string
	root   string
	stored archive.Stored
	older  int
}

// readArchived reads the archives of the project and global hook stores and of
// this repository's check store, newest removal first. A store that cannot be
// read yields a line saying so instead of entries.
func readArchived(root string) ([]archivedEntry, []string) {
	type store struct {
		label, scope, dir, root string
		err                     error
	}
	project, projectErr := claude.GetHooksDir(claude.ProjectScope)
	global, globalErr := claude.GetHooksDir(claude.GlobalScope)
	stores := []store{
		{label: "the project hook store", scope: claude.ProjectScope, dir: project, err: projectErr},
		{label: "the global hook store", scope: claude.GlobalScope, dir: global, err: globalErr},
	}
	if root != "" {
		stores = append(stores, store{label: "this repository's check store",
			scope: claude.ProjectScope, dir: checks.Dir(root), root: root})
	}
	var out []archivedEntry
	var unread []string
	for _, s := range stores {
		entries, err := archive.List(s.dir)
		if s.err != nil {
			err = s.err
		}
		if err != nil {
			unread = append(unread, "The archive of "+s.label+" was not read: "+err.Error())
			continue
		}
		latest := map[string]int{}
		for _, e := range entries {
			key := e.Tier + "/" + e.ID
			if i, ok := latest[key]; ok {
				out[i].stored = e
				out[i].older++
				continue
			}
			latest[key] = len(out)
			out = append(out, archivedEntry{scope: s.scope, root: s.root, stored: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].stored.RemovedAt().After(out[j].stored.RemovedAt())
	})
	return out, unread
}

// archivedRows is one row per archived fix: what it was, where it was removed
// from, when, and the reason the removal kept.
func (m *Model) archivedRows() []fixRow {
	rows := make([]fixRow, 0, len(m.archived))
	for i := range m.archived {
		e := &m.archived[i]
		s := e.stored
		removed := s.Removed
		if t := s.RemovedAt(); !t.IsZero() {
			removed = t.Local().Format("2006-01-02 15:04")
		}
		state := "no reason was given"
		if s.Reason != "" {
			state = "reason: " + s.Reason
		}
		if e.older > 0 {
			state += fmt.Sprintf("; %d earlier removal(s) of this id are archived too, and `steer archive list` shows them", e.older)
		}
		if len(s.Missing) > 0 {
			state += "; missing from the archive: " + strings.Join(s.Missing, ", ") + ", so a restore is refused"
		}
		rows = append(rows, fixRow{kind: fixArchived, name: s.ID, scope: e.scope,
			description: status.ArtifactNoun(s.Tier) + " from the " + e.scope + " store, removed " + removed,
			state:       state, attention: len(s.Missing) > 0, archived: e})
	}
	return rows
}

// rowTier is the tier whose fix a row lists, "" for a row that lists none.
func rowTier(row fixRow) string {
	switch row.kind {
	case guardInstalled:
		return string(tier.Guard)
	case fixRule:
		return string(tier.Instruction)
	case fixAddendum:
		return string(tier.Review)
	case fixCheck:
		return string(tier.Check)
	}
	return ""
}

// archiveKey answers x on the selected row: a fix Steer can archive opens the
// archive form, and a built-in guard says it turns off instead.
func (m *Model) archiveKey(row fixRow) {
	switch {
	case row.builtin:
		m.note("%s is a built-in guard: enter turns it off, and turning it off writes no archive", row.name)
	case row.kind == guardInstalled && row.source == claude.SourceFactory:
		m.note("%s is a guard Steer keeps for itself, so it has no switch here", row.name)
	case !row.removable:
		if row.unremovable != "" {
			m.note("%s", row.unremovable)
		}
	case row.kind == fixCheck && m.checking != "":
		m.note("a check action is running; archive %s after it finishes", row.name)
	default:
		m.archiveForm = &archiveForm{tier: rowTier(row), id: row.name, scope: row.scope}
		m.editing = true
		m.input.Prompt = "reason: "
		m.input.Placeholder = "why this fix is removed (optional)"
		m.input.SetValue("")
		m.input.CursorEnd()
		m.input.Focus()
	}
}

// archiveEditing drives the archive form: enter archives the fix, esc cancels.
func (m *Model) archiveEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.archiveForm = nil
		m.note("cancelled; nothing was archived")
		return m, nil
	case "enter":
		note, err := m.archiveFix(*m.archiveForm, strings.TrimSpace(m.input.Value()))
		if err != nil {
			// Nothing was removed, so the form stays open with the typed reason intact.
			m.note("error: %v", err)
			return m, nil
		}
		m.editing = false
		m.archiveForm = nil
		m.refresh()
		m.note("%s", note)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// archiveFix removes one fix through its tier's shared remove function, the one
// its CLI command calls, and says what the removal did.
func (m *Model) archiveFix(f archiveForm, reason string) (string, error) {
	noun := status.ArtifactNoun(f.tier)
	restore := "; enter on it under Archived restores it"
	switch tier.Kind(f.tier) {
	case tier.Guard:
		res, err := claude.RemoveManagedHook(f.scope, f.id, claude.RemoveOptions{Reason: reason}, m.targets...)
		if err != nil {
			return "", err
		}
		return "archived " + noun + " " + f.id + "; it no longer runs in " + scopeReach(f.scope) +
			fixtureNote(res.Verification, "") + restore, nil
	case tier.Instruction:
		if _, err := claude.RemoveManagedRule(f.scope, f.id, reason, m.targets...); err != nil {
			return "", err
		}
		return "archived " + noun + " " + f.id + "; the instructions in " + scopeReach(f.scope) +
			" no longer carry it" + restore, nil
	case tier.Review:
		if _, err := claude.RemoveAddendum(f.scope, f.id, reason); err != nil {
			return "", err
		}
		if f.scope == claude.ProjectScope {
			return "archived " + noun + " " + f.id + "; " + commitStep("removal") + restore, nil
		}
		return "archived " + noun + " " + f.id + "; the commit gate no longer reads it" + restore, nil
	case tier.Check:
		if _, _, err := checks.Remove(m.snap.Checks.Repo, f.id, reason); err != nil {
			return "", err
		}
		wiring := ""
		if checks.OracleNamesCheck(m.snap.Checks.Repo, f.id) {
			wiring = "; " + checks.OracleName + " still runs it by name and now fails until you remove " + f.id + " from it"
		}
		return "archived " + noun + " " + f.id + "; its cases and validation history stay in this repository" + wiring + restore, nil
	}
	return "", fmt.Errorf("%s has no tier this panel can archive", f.id)
}

// commitStep says when a change to a project addendum entry reaches the commit
// gate, which reads project entries at HEAD.
func commitStep(change string) string {
	manifest := hookstore.ManifestName
	if dir, err := hookstore.Resolve(claude.ProjectScope); err == nil {
		manifest = filepath.Join(dir, hookstore.ManifestName)
	}
	return "the commit gate reads project entries at HEAD, so the " + change +
		" takes effect after you commit " + manifest
}

// archiveConsequence says what archiving the fix in the form removes, where, and
// what the archive keeps.
func archiveConsequence(f archiveForm) string {
	switch tier.Kind(f.tier) {
	case tier.Guard:
		return "This removes the guard from " + scopeReach(f.scope) + ", so it no longer runs there. " +
			"Its file, fixtures and manifest entry go to the archive, and the Archived section can restore them."
	case tier.Instruction:
		return "This removes the rule from the " + f.scope + " store and from the instructions each agent reads in " +
			scopeReach(f.scope) + ". Its text goes to the archive, and the Archived section can restore it."
	case tier.Review:
		out := "This removes the entry from the " + f.scope + " store, so the commit gate's reviewer is no longer told it. " +
			"Its text goes to the archive, and the Archived section can restore it."
		if f.scope == claude.ProjectScope {
			step := commitStep("removal")
			out += " " + strings.ToUpper(step[:1]) + step[1:] + "."
		}
		return out
	case tier.Check:
		return "This drops the check's declaration from this repository. The declaration goes to the archive, " +
			"its cases and validation history stay, and the Archived section can restore it."
	}
	return ""
}

// viewArchiveForm renders the archive form over the selected fix.
func (m *Model) viewArchiveForm() string {
	width := m.bodyWidth()
	f := m.archiveForm
	out := m.title("Archive " + status.ArtifactNoun(f.tier) + " " + f.id + " — enter to archive, esc to cancel")
	out += wrapTo(archiveConsequence(*f), "  ", width)
	m.input.Width = max(1, width-14)
	return out + "\n" + inset(wrapTo(m.input.View(), "", width-3), width, true)
}

// restoreArchived puts the selected entry back through its tier's installer, the
// code `steer archive restore` runs. A check replays its cases first, which can
// take minutes, so it runs as a command like the other check actions.
func (m *Model) restoreArchived(e *archivedEntry) tea.Cmd {
	s := e.stored
	noun := status.ArtifactNoun(s.Tier)
	if s.Tier == string(tier.Check) {
		if m.checking != "" {
			return nil
		}
		m.checking = checkRestore
		m.note("replaying the cases of %s; it is declared again only if they all match…", s.ID)
		root := e.root
		return func() tea.Msg {
			_, run, err := checks.Restore(root, s)
			return checkActionMsg{action: checkRestore, id: s.ID, note: restoreCheckNote(s.ID, run, err)}
		}
	}
	result, err := claude.RestoreArchived(e.scope, s, m.targets...)
	fixtures := ""
	if result != nil {
		fixtures = fixtureNote(result.Verification, s.ID)
	}
	if err != nil {
		m.note("%s %s was not restored, and its archive entry stays: %v%s", noun, s.ID, err, fixtures)
		return nil
	}
	m.refresh()
	note := "restored " + noun + " " + s.ID + " in " + scopeReach(e.scope) + fixtures
	if s.Tier == string(tier.Review) && e.scope == claude.ProjectScope {
		note += "; " + commitStep("restore")
	}
	m.note("%s", note)
	return nil
}

// restoreCheckNote says what a check restore found: the replay's result, and
// whether the check is declared again.
func restoreCheckNote(id string, run checks.ValidationRun, err error) string {
	if err == nil {
		return "restored check " + id + "; " + testNote(id, run, nil)
	}
	if run.Verdict == "" {
		return "check " + id + " was not restored, and its archive entry stays: " + err.Error()
	}
	return "check " + id + " was not restored, and its archive entry stays; " + testNote(id, run, err)
}

// fixtureNote says what a fixture rerun found, as a clause to append: the named
// guard's own result and every guard the rerun could not verify. It is "" when
// there is neither.
func fixtureNote(v *claude.FixtureVerification, id string) string {
	if v == nil {
		return ""
	}
	var parts []string
	for _, r := range v.Reports {
		switch {
		case !r.Tested():
			parts = append(parts, fmt.Sprintf("%s %s", r.Hook, r.NotVerified("guard runner")))
		case r.Hook == id:
			p := fmt.Sprintf("%s intercepted its %d stored case(s)", r.Hook, len(r.Cases))
			if n := r.Shadowed(); n > 0 {
				p += fmt.Sprintf(", %d of them caught first by another guard", n)
			}
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "; fixtures: " + strings.Join(parts, "; ")
}
