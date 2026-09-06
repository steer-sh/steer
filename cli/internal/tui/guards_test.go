package tui

import (
	"strings"
	"testing"

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
	m.active = panelGuards

	if got := strings.Count(m.viewGuards(), "bare-python"); got != 1 {
		t.Errorf("the enabled built-in guard appears %d times, want once", got)
	}
	if !strings.Contains(m.viewGuards(), "no-force-push") {
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
	m.active = panelGuards
	view := m.viewGuards()
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
	m.active = panelGuards
	view := m.viewGuards()
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
	m.active = panelGuards
	if strings.Contains(m.viewGuards(), "Maintenance needed") {
		t.Errorf("a healthy screen asks for maintenance:\n%s", m.viewGuards())
	}
	if got := m.guardsLegend(); !strings.Contains(got, "turn off") {
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
	m.active = panelGuards
	view := m.viewGuards()
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
	rows := m.guardRows()
	repairs := 0
	for _, r := range rows {
		if r.kind == guardRepair {
			repairs++
		}
	}
	if repairs != 2 {
		t.Errorf("%d repair rows, want one per detected issue", repairs)
	}
	m.cursor[panelGuards] = len(rows) - 1
	if got := m.guardsLegend(); !strings.Contains(got, "run this repair") {
		t.Errorf("the legend on a repair row reads %q", got)
	}
}
