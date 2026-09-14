package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guard"
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
		"missing from the instructions of Codex — run: steer hooks rebuild --scope global",
		"fp-sort",
		"removed but not committed; the commit gate still reads it",
		"/w/steer",
		"not authorized to run here",
		"no-count",
		"never replayed here",
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
	if got := m.fixesLegend(); !strings.Contains(got, "replay this one check") {
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
