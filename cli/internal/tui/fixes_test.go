package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/status"
)

// An enabled built-in guard is in your inventory, so it appears there and not a
// second time under the ones you can turn on. Two lines read as two protections.
func TestAnEnabledBuiltInGuardIsListedOnce(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Hooks: []status.HookInfo{{Name: "bare-python", Source: claude.SourceFactory}},
		Presets: []status.PresetInfo{
			{Name: "bare-python", Enabled: true, Description: "blocks bare python"},
			{Name: "no-force-push", Description: "blocks git push --force"},
		},
	}
	m.active = panelFixes

	if got := strings.Count(m.viewFixes(), "bare-python"); got != 1 {
		t.Errorf("the enabled built-in guard appears %d times, want once", got)
	}
	if !strings.Contains(m.viewFixes(), "no-force-push") {
		t.Error("a built-in guard that is off is not offered")
	}
}

// Every guard in your inventory says where it came from, in the reader's words.
func TestEveryGuardNamesItsOrigin(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{Hooks: []status.HookInfo{
		{Name: "written-by-steer", Source: claude.SourceGenerated},
		{Name: "built-in", Source: claude.SourceFactory},
		{Name: "hand-written", Source: claude.SourceManual},
	}}
	m.active = panelFixes
	view := m.viewFixes()
	for _, want := range []string{
		"written for you by Steer", "built in to Steer", "written by you, registered with Steer",
	} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the inventory is missing the origin %q:\n%s", want, view)
		}
	}
	// The inventory is Steer's own registrations, never every hook the agent runs.
	if !strings.Contains(view, "It is not every hook your coding agent runs") {
		t.Error("the inventory claims to hold every hook")
	}
}

// An empty inventory is empty HERE. It establishes nothing about the other
// location or another machine.
func TestAnEmptyInventoryIsLocal(t *testing.T) {
	m := New(gate.Defaults(), claude.ProjectScope)
	m.width = 200
	m.snap = &status.SystemStatus{}
	m.active = panelFixes
	view := m.viewFixes()
	if !strings.Contains(view, "No guards recorded for this project") {
		t.Errorf("the empty state does not name the location:\n%s", view)
	}
	if !strings.Contains(view, "your other machines, are not read here") {
		t.Errorf("the empty state does not say what it did not read:\n%s", view)
	}
}

// A healthy screen asks for no maintenance: the two repairs appear only beside
// the issue that needs them.
func TestNoMaintenanceIsAskedForOnAHealthyScreen(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Hooks:   []status.HookInfo{{Name: "bare-python", Source: claude.SourceFactory}},
		Presets: []status.PresetInfo{{Name: "bare-python", Enabled: true}},
		Posture: status.Posture{Masters: []status.MasterRuntime{{
			Harness: "claude-code", Scope: "global", State: status.MasterCurrent,
			Runtime: guard.RuntimeDigest(),
		}}},
	}
	m.active = panelFixes
	if strings.Contains(m.viewFixes(), "Maintenance needed") {
		t.Errorf("a healthy screen asks for maintenance:\n%s", m.viewFixes())
	}
	if got := m.fixesLegend(); !strings.Contains(got, "turn off") {
		t.Errorf("the legend on an installed guard reads %q", got)
	}
}

// Only a guard the catalog can turn back on offers "turn off". A generated or
// hand-written guard has no copy to restore, and a guard Steer keeps for itself
// is not a built-in the reader can switch, so the legend promised an action enter
// then refused.
func TestOnlyABuiltInGuardOffersTurnOff(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Hooks: []status.HookInfo{
			{Name: "bare-python", Source: claude.SourceFactory},
			{Name: "steer-self-protection", Source: claude.SourceFactory},
			{Name: "written-by-steer", Source: claude.SourceGenerated},
			{Name: "hand-written", Source: claude.SourceManual},
		},
		Presets: []status.PresetInfo{{Name: "bare-python", Enabled: true}},
	}
	m.active = panelFixes
	want := map[string]bool{"bare-python": true}
	for i, row := range m.fixRows() {
		if row.kind != guardInstalled {
			continue
		}
		m.cursor[panelFixes] = i
		if got := strings.Contains(m.fixesLegend(), "turn off"); got != want[row.name] {
			t.Errorf("%s: legend offers turn off = %v, want %v", row.name, got, want[row.name])
		}
		if want[row.name] {
			continue
		}
		m.message = ""
		m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter})
		if strings.Contains(m.message, "is off") {
			t.Errorf("%s: enter turned a guard off: %q", row.name, m.message)
		}
		if row.name == "steer-self-protection" && !strings.Contains(m.message, "keeps for itself") {
			t.Errorf("enter on a guard Steer keeps for itself reads %q", m.message)
		}
	}
}

// x archives every fix except a built-in guard, which turns off and writes no
// archive. A guard Steer keeps for itself, and a project entry already removed on
// disk, offer neither.
func TestArchiveIsOfferedOnEveryFixButABuiltInGuard(t *testing.T) {
	m := fixesModel(false)
	m.snap.Hooks = []status.HookInfo{
		{Name: "bare-python", Source: claude.SourceFactory},
		{Name: "steer-self-protection", Source: claude.SourceFactory},
		{Name: "written-by-steer", Source: claude.SourceGenerated},
		{Name: "hand-written", Source: claude.SourceManual},
	}
	m.snap.Presets = []status.PresetInfo{{Name: "bare-python", Enabled: true}}
	m.snap.Addenda = append(m.snap.Addenda, status.AddendumInfo{ID: "fp-live", Scope: "global",
		Text: "a live entry", State: status.AddendumLive})
	want := map[string]bool{"written-by-steer": true, "hand-written": true, "config-path": true,
		"fp-live": true, "no-count": true}
	for i, row := range m.fixRows() {
		m.cursor[panelFixes] = i
		if got := strings.Contains(m.fixesLegend(), "x archive"); got != want[row.name] {
			t.Errorf("%s (%s): legend offers archive = %v, want %v", row.name, row.kind, got, want[row.name])
		}
		m.fixesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		if m.editing != want[row.name] {
			t.Errorf("%s (%s): x opened the archive form = %v", row.name, row.kind, m.editing)
		}
		if row.builtin && !strings.Contains(m.message, "writes no archive") {
			t.Errorf("x on a built-in guard reads %q", m.message)
		}
		if m.editing {
			m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		}
	}
}

// The form says what the archive keeps before enter removes anything, and a
// project entry's form says the removal waits for a commit.
func TestTheArchiveFormSaysWhatItRemovesAndWhen(t *testing.T) {
	m := fixesModel(false)
	m.snap.Addenda = []status.AddendumInfo{{ID: "fp-live", Scope: "project",
		Text: "a live entry", HeadText: "a live entry", State: status.AddendumLive}}
	selectKind(t, m, fixAddendum)
	m.fixesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	view := strings.Join(strings.Fields(m.View()), " ")
	for _, want := range []string{"Archive review prompt entry fp-live", "the Archived section can restore it",
		"restore it. The commit gate reads project entries at HEAD, so the removal takes effect after you commit",
		"enter archive · esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the form is missing %q:\n%s", want, view)
		}
	}
	if global := archiveConsequence(archiveForm{tier: "review", id: "fp", scope: "global"}); strings.Contains(global, "after you commit") {
		t.Errorf("a global entry's form asks for a commit: %q", global)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing || !strings.Contains(m.message, "nothing was archived") {
		t.Errorf("esc left editing = %v with note %q", m.editing, m.message)
	}
}

// A rule archived from the panel lands in the store's archive with its reason,
// and enter on its Archived row installs it again. A second restore is refused
// while the rule is back, and the panel shows the refusal.
func TestARuleArchivedFromThePanelIsRestoredFromIt(t *testing.T) {
	const id = "tui-archive-round-trip"
	if _, err := claude.InstallManagedRule("global", id, "resolve config from the repo root"); err != nil {
		t.Fatal(err)
	}
	store, err := claude.GetHooksDir("global")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = claude.RemoveManagedRule("global", id, "")
		_ = os.RemoveAll(filepath.Join(store, archive.DirName, id))
	})
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.active = panelFixes
	selectRow(t, m, fixRule, id)
	m.fixesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.input.SetValue("blocks a legitimate path")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing || !strings.HasPrefix(m.message, "archived rule "+id) {
		t.Fatalf("archive left editing = %v with note %q", m.editing, m.message)
	}
	entries, err := archive.ForID(store, id)
	if err != nil || len(entries) != 1 || entries[0].Reason != "blocks a legitimate path" {
		t.Fatalf("archive entries = %+v, %v; want one carrying the reason", entries, err)
	}

	selectRow(t, m, fixArchived, id)
	m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.HasPrefix(m.message, "restored rule "+id) {
		t.Fatalf("restore reads %q", m.message)
	}
	rules, err := claude.ListManagedRules("global")
	if err != nil || len(rules) == 0 {
		t.Fatalf("the rule is not back: %+v, %v", rules, err)
	}

	selectRow(t, m, fixArchived, id)
	m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.message, "was not restored") {
		t.Errorf("a restore over the installed rule reads %q", m.message)
	}
}

// An id archived twice in one store is one row: the newer removal, with the
// earlier one counted.
func TestAnIDArchivedTwiceShowsItsNewerRemoval(t *testing.T) {
	const id = "tui-archived-twice"
	store, err := claude.GetHooksDir("global")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, archive.DirName, id)) })
	for _, reason := range []string{"first removal", "second removal"} {
		if _, err := archive.Write(store, archive.Entry{Tier: "instruction", ID: id, Scope: "global", Reason: reason}, nil); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := readArchived("")
	var found []archivedEntry
	for _, e := range entries {
		if e.stored.ID == id {
			found = append(found, e)
		}
	}
	if len(found) != 1 || found[0].stored.Reason != "second removal" || found[0].older != 1 {
		t.Fatalf("archived rows for %s = %+v; want one carrying the second removal and one earlier", id, found)
	}
}

// A check restore replays cases, so it runs as a command, and only one check
// action runs at a time.
func TestACheckRestoreRunsAsACheckAction(t *testing.T) {
	m := fixesModel(false)
	m.archived = []archivedEntry{{scope: "project", root: "/w/steer",
		stored: archive.Stored{Entry: archive.Entry{Tier: "check", ID: "old-check"}}}}
	selectRow(t, m, fixArchived, "old-check")
	if got := m.fixesLegend(); !strings.Contains(got, "enter restore") {
		t.Errorf("the archived check's legend reads %q", got)
	}
	if _, cmd := m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || m.checking != checkRestore {
		t.Fatalf("enter on an archived check started nothing (checking %q)", m.checking)
	}
	if got := m.fixesLegend(); strings.Contains(got, "restore") {
		t.Errorf("the legend offers %q while a check action runs", got)
	}
	selectKind(t, m, fixCheck)
	if strings.Contains(m.fixesLegend(), "x archive") {
		t.Error("a check offers archive while a check action runs")
	}
}

// A restore's note names the fixture result of the restored guard and every
// guard the rerun could not verify.
func TestAFixtureNoteNamesTheRestoredGuardAndEveryFailure(t *testing.T) {
	v := &claude.FixtureVerification{Reports: []fixture.Report{
		{Hook: "alpha", Cases: []fixture.CaseResult{{IndividualPass: true, MasterState: fixture.MasterVerified}}},
		{Hook: "beta", Cases: []fixture.CaseResult{{IndividualPass: true, MasterState: fixture.MasterVerified}}},
		{Hook: "gamma", Cases: []fixture.CaseResult{{IndividualPass: false, MasterState: fixture.MasterVerified}}},
	}}
	got := fixtureNote(v, "alpha")
	for _, want := range []string{"alpha intercepted its 1 stored case(s)", "gamma NOT verified: 1 case(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "beta") {
		t.Errorf("the note names a guard that passed and was not restored: %q", got)
	}
	if got := restoreCheckNote("no-count", checks.ValidationRun{Verdict: checks.VerdictFailed, Reason: "the defect case passed"},
		errors.New("not validated")); !strings.Contains(got, "was not restored") || !strings.Contains(got, "the defect case passed") {
		t.Errorf("a refused check restore reads %q", got)
	}
}

// selectRow puts the cursor on the row of a kind with a name.
func selectRow(t *testing.T, m *Model, kind, name string) {
	t.Helper()
	for i, r := range m.fixRows() {
		if r.kind == kind && r.name == name {
			m.cursor[panelFixes] = i
			return
		}
	}
	t.Fatalf("no %s row named %s", kind, name)
}

// Each detected issue names its own operation, what it changes and where. A
// footer key called "sync" named none of the three.
func TestEachIssueNamesItsOperationAndItsReach(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{
		Hooks:   []status.HookInfo{{Name: "bare-python", Source: claude.SourceFactory}},
		Presets: []status.PresetInfo{{Name: "bare-python", Enabled: true, Drifted: true}},
		Posture: status.Posture{Masters: []status.MasterRuntime{{
			Harness: "codex", Scope: "global", State: status.MasterStale,
			Runtime: "deadbeef", Expected: guard.RuntimeDigest(),
		}}},
	}
	m.active = panelFixes
	view := m.viewFixes()
	for _, want := range []string{
		"Update built-in guards",
		"bare-python no longer matches the built-in guard",
		"Local edits to it are replaced",
		"Update the guard runner for Codex",
		"every project on this machine",
		"Your guard files are left alone",
	} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("an issue is missing %q:\n%s", want, view)
		}
	}
	// Both repairs are selectable, so enter reaches each one.
	rows := m.fixRows()
	repairs := 0
	for _, r := range rows {
		if r.kind == guardRepair {
			repairs++
		}
	}
	if repairs != 2 {
		t.Errorf("%d repair rows, want one per detected issue", repairs)
	}
	m.cursor[panelFixes] = len(rows) - 1
	if got := m.fixesLegend(); !strings.Contains(got, "run this repair") {
		t.Errorf("the legend on a repair row reads %q", got)
	}
}

// fixesModel is a dashboard over a snapshot holding one fix of each tier beside
// guards: a rule one agent lacks, a project entry removed on disk but still
// committed, and a repository with one declared check.
func fixesModel(adopted bool) *Model {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{
		Posture: status.Posture{Rules: []status.RulesPresence{
			{Harness: "claude-code", Scope: "global", State: status.RulesCurrent},
			{Harness: "codex", Scope: "global", State: status.RulesDrifted, Missing: []string{"config-path"}},
		}},
		Rules: []status.RuleInfo{{ID: "config-path", Scope: "global", Text: "resolve config from the repo root"}},
		Addenda: []status.AddendumInfo{{ID: "fp-sort", Scope: "project",
			HeadText: "sorted output is not a finding", State: status.AddendumUncommittedRemoval}},
		Checks: status.CheckTier{Repo: "/w/steer", Manifest: true, Adopted: adopted,
			Checks: []status.CheckRow{{ID: "no-count", Kind: "script", Binding: ".steer/checks/no-count.sh",
				Validation: status.ValidationNone}}},
	}
	m.active = panelFixes
	return m
}

// selectKind puts the cursor on the first row of a kind.
func selectKind(t *testing.T, m *Model, kind string) {
	t.Helper()
	for i, r := range m.fixRows() {
		if r.kind == kind {
			m.cursor[panelFixes] = i
			return
		}
	}
	t.Fatalf("no %s row", kind)
}

// Each tier's row says where its fix stands in that tier's terms: which agents'
// instructions carry a rule, what the commit gate reads for an entry, and how a
// check's proof and this repository's authorization stand.
func TestEachTierNamesItsOwnState(t *testing.T) {
	view := strings.Join(strings.Fields(fixesModel(false).viewFixes()), " ")
	for _, want := range []string{
		"read by Claude Code",
		"missing from the instructions of Codex — run: steer hooks sync --scope global",
		"fp-sort",
		"removed but not committed; the commit gate still reads it",
		"/w/steer",
		"not authorized to run here",
		"no-count",
		"never tested here",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel is missing %q:\n%s", want, view)
		}
	}
}

// The repository row authorizes the whole repository and says it runs check.sh;
// a check row replays that one check. An adopted repository offers nothing to
// authorize, and while an action runs no row starts another.
func TestCheckActionsNameTheirScopeAndRunOneAtATime(t *testing.T) {
	m := fixesModel(false)
	selectKind(t, m, fixRepo)
	if got := m.fixesLegend(); !strings.Contains(got, "authorize the whole repository (runs check.sh)") {
		t.Errorf("the repository row's legend reads %q", got)
	}
	selectKind(t, m, fixCheck)
	if got := m.fixesLegend(); !strings.Contains(got, "test this one check") {
		t.Errorf("the check row's legend reads %q", got)
	}
	if _, cmd := m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || m.checking != checkTest {
		t.Fatalf("enter on a check row started nothing (checking %q)", m.checking)
	}
	selectKind(t, m, fixRepo)
	if _, cmd := m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("a second check action started while one was running")
	}
	if got := m.fixesLegend(); got != "" {
		t.Errorf("the legend offers %q while an action runs", got)
	}

	m = fixesModel(true)
	selectKind(t, m, fixRepo)
	if _, cmd := m.fixesKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.fixesLegend() != "" {
		t.Error("an adopted repository offers to be authorized again")
	}
}

// A finished action's note says what happened: a refused adoption is not an
// authorization, and a replay names its verdict.
func TestCheckActionNotesNameTheOutcome(t *testing.T) {
	if got := adoptNote("/w/steer", checks.AdoptResult{}, errors.New("check.sh failed")); !strings.HasPrefix(got, "not authorized: ") {
		t.Errorf("a refused adoption reads %q", got)
	}
	if got := adoptNote("/w/steer", checks.AdoptResult{Already: true}, nil); !strings.Contains(got, "already authorized") {
		t.Errorf("an adopted repository reads %q", got)
	}
	if got := testNote("no-count", checks.ValidationRun{Verdict: checks.VerdictFailed, Reason: "the defect case passed"},
		errors.New("not validated")); got != "no-count failed: the defect case passed" {
		t.Errorf("a failed replay reads %q", got)
	}
	if got := testNote("no-count", checks.ValidationRun{Verdict: checks.VerdictValidated}, nil); !strings.Contains(got, "failed the defect case") {
		t.Errorf("a validated replay reads %q", got)
	}
}

// A refused adoption carries the facts the attempt returned: check.sh's own ending,
// the checks that did not pass worst first, and the last lines it printed when
// nothing structured explains the failure. The note is the only place these reach a
// reader, because the adoption's evidence directory does not outlive the call.
func TestARefusedAdoptionNamesTheOracleAndTheChecksThatDidNotPass(t *testing.T) {
	exit := 1
	res := checks.AdoptResult{
		Oracle: checks.OracleRun{RunID: "r1", Outcome: checks.OutcomeFailed, ExitCode: &exit,
			Excerpt: "go test ./...\n\n--- FAIL: TestThing\n"},
		Executions: []checks.Execution{
			{CheckID: "unreached", Outcome: checks.OutcomeNotExecuted, Reason: "check.sh stopped before this check reported"},
			{CheckID: "ok", Outcome: checks.OutcomePassed},
			{CheckID: "no-count", Outcome: checks.OutcomeFailed},
		},
	}
	got := adoptNote("/w/steer", res, errors.New("adoption refused: check.sh failed"))
	for _, want := range []string{"not authorized: ", "steer check adopt /w/steer",
		"check.sh failed with exit 1", "no-count failed", "| --- FAIL: TestThing"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ok passed") {
		t.Errorf("the note lists a check that passed:\n%s", got)
	}
	if strings.Index(got, "unreached") < strings.Index(got, "no-count") {
		t.Errorf("a check the oracle never reached sorts before a failure:\n%s", got)
	}
}

// The note stays bounded whatever the repository holds: it counts the rows it drops,
// and it leaves the excerpt out where a reason already explains the failure. The
// rows themselves are never conditional on that reason.
func TestARefusedAdoptionNoteStaysBounded(t *testing.T) {
	var many []checks.Execution
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		many = append(many, checks.Execution{CheckID: id, Outcome: checks.OutcomeFailed})
	}
	got := adoptNote("/w/steer", checks.AdoptResult{
		Oracle:     checks.OracleRun{RunID: "r1", Outcome: checks.OutcomeFailed},
		Executions: many,
	}, errors.New("adoption refused"))
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("five failures are not counted down to three:\n%s", got)
	}

	got = adoptNote("/w/steer", checks.AdoptResult{
		Oracle: checks.OracleRun{RunID: "r1", Outcome: checks.OutcomeTimeout,
			Reason: "the deadline stopped check.sh", Excerpt: "still running\n"},
	}, errors.New("adoption refused"))
	if !strings.Contains(got, "the deadline stopped check.sh") || strings.Contains(got, "still running") {
		t.Errorf("an explained failure carries its excerpt anyway:\n%s", got)
	}
}

// An adoption stopped by a check's own cases never reaches check.sh, so the note
// names the verdict instead. An integration failure names the remedy.
func TestARefusedAdoptionNamesAValidationVerdictAndTheIntegrationRemedy(t *testing.T) {
	got := adoptNote("/w/steer", checks.AdoptResult{
		Validations: []checks.AdoptValidation{
			{CheckID: "reused", Reused: true, Run: checks.ValidationRun{CheckID: "reused", Verdict: checks.VerdictValidated}},
			{CheckID: "no-count", Run: checks.ValidationRun{CheckID: "no-count",
				Verdict: checks.VerdictUnavailable, Reason: "the case file is missing"}},
		},
	}, errors.New("adoption refused: no-count did not validate"))
	if !strings.Contains(got, "no-count unavailable: the case file is missing") {
		t.Errorf("the note misses the validation verdict:\n%s", got)
	}
	if strings.Contains(got, "reused") {
		t.Errorf("the note lists a reused receipt:\n%s", got)
	}

	got = adoptNote("/w/steer", checks.AdoptResult{
		Oracle: checks.OracleRun{RunID: "r1", Outcome: checks.OutcomePassed,
			Missing: []string{"no-count"}, IntegrationFailure: true},
	}, errors.New("adoption refused: check.sh passed without running no-count"))
	if !strings.Contains(got, checks.ExecInvocation) || !strings.Contains(got, "steer check guide") {
		t.Errorf("an integration failure names no remedy:\n%s", got)
	}
}

// x on a row whose tier the panel does archive says why it cannot archive this one,
// so the key is never silent on a row marked for attention. An entry the working
// tree no longer holds has nothing left to archive, and a row standing for a store
// names no entry at all.
func TestXSaysWhyAnEntryOffDiskCannotBeArchived(t *testing.T) {
	m := fixesModel(false)
	selectRow(t, m, fixAddendum, "fp-sort")
	m.message = ""
	m.fixesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.editing {
		t.Fatal("x opened the archive form over an entry the working tree does not hold")
	}
	for _, want := range []string{"fp-sort", "nothing left to archive", "after you commit"} {
		if !strings.Contains(m.message, want) {
			t.Errorf("x on an entry removed on disk lacks %q: %q", want, m.message)
		}
	}

	m.snap.Addenda = []status.AddendumInfo{{Scope: "project", State: status.AddendumUnreadable, Detail: "boom"}}
	selectRow(t, m, fixAddendum, "the project review prompt")
	m.message = ""
	m.fixesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.editing || !strings.Contains(m.message, "not one entry") {
		t.Errorf("x on a row standing for a store: editing %v, note %q", m.editing, m.message)
	}
}
