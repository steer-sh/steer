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
		After:  map[string]any{"eligible": 3, "unknown": 1, "unknown_scope": 1},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || !strings.Contains(g.Sentence, "no repository or changed files") {
		t.Fatalf("gap = %+v", g)
	}
}

// A ledger cause names itself and carries no command: an overdue row is read by the
// resolver's own status entry, and a closed miss has no repair.
func TestALedgerCauseIsDisclosedWithNoCommand(t *testing.T) {
	scope := &cadence.Scope{Version: 1, All: true}
	for _, tc := range []struct {
		name, want string
		after      map[string]any
	}{
		{"overdue", "no outcome read yet", map[string]any{"unknown": 1, "unknown_evidence": 1, "ledger_overdue": 1}},
		{"missed", "no decision in any session history", map[string]any{"unknown": 1, "unknown_evidence": 1, "ledger_missed": 1}},
		{"pending", "await a decision", map[string]any{"unknown": 1, "unknown_evidence": 1, "pending": 1}},
	} {
		obs := mine.Observation{Coverage: mine.ObservationCoverage{After: tc.after}}
		g := EvidenceGap(obs, scope)
		if g == nil || g.Command != "" || !strings.Contains(g.Sentence, tc.want) {
			t.Errorf("%s: gap = %+v", tc.name, g)
		}
	}
}

// Overdue work outranks a closed miss, and a closed miss outranks pending, across
// both windows.
func TestLedgerCausesRankOverdueThenMissedThenPending(t *testing.T) {
	scope := &cadence.Scope{Version: 1, All: true}
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		Before: map[string]any{"unknown": 2, "unknown_evidence": 2, "ledger_missed": 1, "pending": 1},
		After:  map[string]any{"unknown": 1, "unknown_evidence": 1, "ledger_overdue": 1},
	}}
	if g := EvidenceGap(obs, scope); g == nil || !strings.Contains(g.Sentence, "no outcome read yet") {
		t.Errorf("overdue work was hidden: %+v", g)
	}
	obs.Coverage.After = map[string]any{"unknown": 1, "unknown_evidence": 1, "pending": 1}
	if g := EvidenceGap(obs, scope); g == nil || !strings.Contains(g.Sentence, "no decision in any session history") {
		t.Errorf("a miss read as settling: %+v", g)
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

// A cause that asks for the reader outranks a ledger disclosure in the other window.
func TestAScopeCauseOutranksALedgerCauseInTheOtherWindow(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{
		Before: map[string]any{"eligible": 4, "unknown": 1, "unknown_evidence": 1, "ledger_overdue": 1},
		After:  map[string]any{"eligible": 3, "unknown": 1, "unknown_scope": 1},
	}}
	g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if g == nil || !strings.Contains(g.Sentence, "no repository or changed files") {
		t.Fatalf("a ledger disclosure hid a scope gap: %+v", g)
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
