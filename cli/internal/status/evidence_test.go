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

func TestEvidenceGapDistinguishesPendingAndMissingAudit(t *testing.T) {
	obs := mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"pending": 2}}}
	if g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true}); g == nil || g.Command != "" {
		t.Fatalf("pending gap = %+v", g)
	}
	obs = mine.Observation{Firing: mine.FiringActivity{Applicable: true, LogPresent: false}}
	if g := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true}); g == nil || !strings.Contains(g.Sentence, "audit log") {
		t.Fatalf("audit gap = %+v", g)
	}
}
