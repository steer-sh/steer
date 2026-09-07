package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

func TestEvidenceGapNamesCoverageCauseWithEligibleWork(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		Before: map[string]any{"eligible": 4},
		After:  map[string]any{"eligible": 3, "unknown_evidence": 1},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || !strings.Contains(g.Sentence, "no recorded decision") {
		t.Fatalf("gap = %+v", g)
	}
	if g.Command != BackfillCommand {
		t.Fatalf("command = %q", g.Command)
	}
}

// A pending row is counted inside unknown_evidence (mine/scope.go:265), so the
// two never arrive apart. A window whose unknowns are ALL pending settles on its
// own, and naming it a missing decision hands the reader a backfill that clears
// nothing; only the excess over pending is a row the resolver closed as gone.
func TestPendingRowsAreNotReportedAsMissingDecisions(t *testing.T) {
	all := mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"eligible": 3, "unknown": 2, "unknown_evidence": 2, "pending": 2},
	}}
	g := EvidenceGap(all, &cadence.Scope{Version: 1, All: true})
	if g == nil || g.Command != "" {
		t.Fatalf("an all-pending window named a command: %+v", g)
	}
	if !strings.Contains(g.Sentence, "await a decision") {
		t.Errorf("an all-pending window is phrased as %q", g.Sentence)
	}
	// One unknown beyond the pending share is a ledger the resolver closed as gone.
	excess := mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"eligible": 3, "unknown": 3, "unknown_evidence": 3, "pending": 2},
	}}
	if g := EvidenceGap(excess, &cadence.Scope{Version: 1, All: true}); g == nil || g.Command != BackfillCommand {
		t.Fatalf("an unknown beyond the pending share named no backfill: %+v", g)
	}
}

// A permanent cause outranks a transient one INSIDE one window. Scope metadata no
// row carried never arrives; a pending row does, on its own.
func TestAPermanentCauseOutranksPendingInOneWindow(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"eligible": 3, "unknown": 3, "unknown_evidence": 2, "pending": 2, "unknown_scope": 1},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || !strings.Contains(g.Sentence, "no repository or changed files") {
		t.Fatalf("a permanent scope gap was masked by pending rows: %+v", g)
	}
}

// Between two PERMANENT causes the one carrying a command wins, whichever window
// holds it. Ranking by window instead would hand the reader the gap they can do
// nothing about and hide the one they can clear.
func TestTheRepairableCauseOutranksTheOneWithNoCommand(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		Before: map[string]any{"eligible": 4, "unknown": 1, "unknown_scope": 1},
		After:  map[string]any{"eligible": 3, "unknown": 2, "unknown_evidence": 2},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || g.Command != BackfillCommand {
		t.Fatalf("the only repairable cause was hidden behind one with no command: %+v", g)
	}
}

// A permanent cause outranks a transient one ACROSS the two windows. A single
// loop returns on the first window that yields anything, so a merely-pending
// before-window would hide the after-window's repairable one.
func TestAPermanentCauseOutranksPendingInTheOtherWindow(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		Before: map[string]any{"eligible": 4, "unknown": 1, "unknown_evidence": 1, "pending": 1},
		After:  map[string]any{"eligible": 3, "unknown": 3, "unknown_evidence": 3},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || g.Command != BackfillCommand {
		t.Fatalf("a pending before-window hid the after-window's backfill: %+v", g)
	}
}

// A chain window's `unknown` is its two named causes summed, so a pending-only
// chain window must not read as a session that recorded no project.
func TestAChainWindowNeverReadsAsAProjectlessSession(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"eligible": 3, "unknown": 2, "unknown_evidence": 2, "pending": 2},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || strings.Contains(g.Sentence, "no project") {
		t.Fatalf("a chain window was named as a projectless session: %+v", g)
	}
	// A session map writes neither named cause, so its whole bucket is its own.
	sess := mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"eligible": 3, "unknown": 2},
	}}
	if g := EvidenceGap(sess, &cadence.Scope{Version: 1, All: true}); g == nil || !strings.Contains(g.Sentence, "no project") {
		t.Fatalf("a session window lost its own cause: %+v", g)
	}
}

func TestEvidenceGapDistinguishesPendingAndMissingAudit(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"unknown_evidence": 2, "pending": 2}}}
	if g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true}); g == nil || g.Command != "" {
		t.Fatalf("pending gap = %+v", g)
	}
	obs = mine.Observation{Firing: mine.FiringActivity{Applicable: true, LogPresent: false}}
	if g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true}); g == nil || !strings.Contains(g.Sentence, "audit log") {
		t.Fatalf("audit gap = %+v", g)
	}
}
