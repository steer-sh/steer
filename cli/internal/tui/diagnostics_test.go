package tui

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/status"
)

// A clean screen says these checks found nothing. It does not say the system
// works: the page runs a fixed set of checks and reads nothing else.
func TestACleanScreenClaimsOnlyItsOwnChecks(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{}
	m.active = panelDiagnostics
	view := m.viewDiagnostics()
	if !strings.Contains(view, "No issues detected by these checks") {
		t.Errorf("the clean state does not name its own coverage:\n%s", view)
	}
	if strings.Contains(view, "everything") {
		t.Errorf("the clean state claims more than it checked:\n%s", view)
	}
}

// Each issue names its consequence and the panel that repairs it.
func TestEachIssueNamesWhereItIsRepaired(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{Posture: status.Posture{
		Unresolved: 3,
		Masters: []status.MasterRuntime{{
			Harness: "claude-code", Scope: "project", State: status.MasterUnknown,
			Expected: guard.RuntimeDigest(),
		}},
	}}
	m.active = panelDiagnostics
	view := m.viewDiagnostics()
	for _, want := range []string{
		"3 reviews have no outcome yet",
		"Open Arming",
		"The guard runner for Claude Code in this project is out of date",
		"Open Fixes",
	} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the issue list is missing %q:\n%s", want, view)
		}
	}
}

// Each history count appears once, with the noun it counts. The review count was
// printed twice, once as a corpus size and once as telemetry.
func TestHistoryCountsAppearOnceWithTheirNoun(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{Telemetry: status.Telemetry{
		ReviewChains: 41, SessionSnapshots: 12, DenialsRows: 5, AuditFirings: 7,
	}}
	m.active = panelDiagnostics
	view := m.viewDiagnostics()
	for _, want := range []string{
		"review sequences", "agent sessions", "actions blocked by auto mode", "guard blocks",
	} {
		if got := strings.Count(view, want); got != 1 {
			t.Errorf("%q appears %d times, want once:\n%s", want, got, view)
		}
	}
	if strings.Contains(view, "Telemetry") {
		t.Error("the separate telemetry block survives")
	}
}

// An absent count says it is absent HERE, and it is not a health signal either
// way: a stored row proves something was recorded, never that it is current.
func TestAnAbsentCountIsLocal(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{}
	m.active = panelDiagnostics
	view := m.viewDiagnostics()
	if !strings.Contains(view, "none recorded here") {
		t.Errorf("an empty count does not say where it is empty:\n%s", view)
	}
	if !strings.Contains(view, "recorded on THIS machine") {
		t.Errorf("the counts do not say they are local:\n%s", view)
	}
}

// A lockfile that is not there is not a fault. One that IS there and will not
// parse is a fault, and reporting it as absent hid a broken file at the path.
func TestAMalformedTeamFileIsNotAnAbsentOne(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.active = panelDiagnostics

	m.snap = &status.SystemStatus{}
	if !strings.Contains(m.viewDiagnostics(), "Not configured for this project") {
		t.Error("an absent lockfile does not read as absent")
	}

	m.snap = &status.SystemStatus{Team: status.TeamState{
		Present: true, Path: "steer-team.lock.json",
		Error: "unexpected end of JSON input",
	}}
	view := m.viewDiagnostics()
	if strings.Contains(view, "Not configured") {
		t.Errorf("a broken lockfile reads as absent:\n%s", view)
	}
	if !strings.Contains(view, "unexpected end of JSON input") {
		t.Errorf("the actual error is not reported:\n%s", view)
	}
}

// Feedback belongs to the panel whose action wrote it. A Reviewer cancellation
// showing on Diagnostics reads as that page's own state.
func TestFeedbackStaysOnItsOwnPanel(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{}
	m.active = panelReviewer
	m.note("cancelled")
	if m.visibleMessage() != "cancelled" {
		t.Fatal("the panel does not show its own feedback")
	}
	m.updateNav(keyOf("tab"))
	if got := m.visibleMessage(); got != "" {
		t.Errorf("the feedback followed the reader to another panel: %q", got)
	}
}

// A read that finishes after the reader moved away names the panel that started
// it, so the note is waiting when they come back rather than lost.
func TestAnAsyncNoteNamesItsOwnPanel(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{}
	m.active = panelDiagnostics
	m.outcomesRead(outcomesReadMsg{before: 2})
	if m.visibleMessage() != "" {
		t.Error("the arming panel's note rendered on diagnostics")
	}
	m.active = panelChannels
	if m.visibleMessage() == "" {
		t.Error("the note was lost rather than filed under the panel that started it")
	}
}

// The technical block is behind t. Storage paths, runner versions and how the
// evidence was produced support inspection; none is needed to read the issues.
func TestDiagnosticsTechnicalBlockHidesBehindAKey(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.width = 200
	m.snap = &status.SystemStatus{
		Corpora: []status.CorpusRow{{Corpus: "commit-review", Present: true, Size: 3,
			Path: "/abs/state/commit-review"}},
		Posture: status.Posture{Reviewer: "agy/gemini-3.7-flash", Instrument: "agy/2/abc/v1"},
	}
	m.active = panelDiagnostics

	if strings.Contains(m.viewDiagnostics(), "/abs/state/commit-review") {
		t.Error("a storage path shows before t")
	}
	m.panelKey(keyOf("t"))
	view := m.viewDiagnostics()
	for _, want := range []string{"/abs/state/commit-review", "agy/gemini-3.7-flash", "agy/2/abc/v1"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the technical block is missing %q:\n%s", want, view)
		}
	}
}
