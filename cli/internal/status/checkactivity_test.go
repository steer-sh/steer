package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// The check line says what ran on this machine, says unknown rather than zero
// when the machine holds no evidence, and names that as the gap.
func TestCheckActivityIsPhrasedAndItsMissingEvidenceIsTheGap(t *testing.T) {
	bound := mine.CheckActivity{Applicable: true, Attribution: mine.AttributionCheckID,
		StreamPresent: true, CheckID: "no-count", Scope: mine.FiringScopeWindowPeriod,
		Executions: 3, Outcomes: map[string]int{"failed": 1, "passed": 2}, Blocked: 1, ChangedDetector: 1}
	got := CheckActivityPhrase(mine.Observation{Checks: bound})
	for _, want := range []string{"on this machine", "3 of no-count", "failed 1, passed 2", "1 stopped a turn", "1 ran an edited check"} {
		if !strings.Contains(got, want) {
			t.Errorf("phrase %q lacks %q", got, want)
		}
	}
	if got := CheckActivityPhrase(mine.Observation{}); got != "" {
		t.Errorf("a fix that is not a check phrases %q, want nothing", got)
	}

	missing := bound
	missing.StreamPresent = false
	obs := mine.Observation{Checks: missing}
	if got := CheckActivityPhrase(obs); !strings.Contains(got, "unknown") {
		t.Errorf("missing evidence phrases %q, want unknown", got)
	}
	gap := EvidenceGap(obs, &cadence.Scope{Version: 1, All: true})
	if gap == nil || !strings.Contains(gap.Sentence, "no check evidence") {
		t.Errorf("gap = %+v, want the missing check evidence", gap)
	}
}
