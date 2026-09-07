package status

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

func actionObs(occurred bool, eligible int) mine.Observation {
	return mine.Observation{
		Recurrence: mine.Recurrence{Occurred: occurred},
		Coverage:   mine.ObservationCoverage{After: map[string]any{"eligible": eligible}},
	}
}

func TestActionForUsesObservationFields(t *testing.T) {
	scope := &cadence.Scope{Version: 1, All: true}
	if got := ActionFor(actionObs(true, 10), scope); got != ActionInspect {
		t.Fatalf("recurrence action = %q", got)
	}
	if got := ActionFor(actionObs(false, 10), scope); got != ActionObserve {
		t.Fatalf("quiet action = %q", got)
	}
	if got := ActionFor(actionObs(false, 0), scope); got != ActionObserve {
		t.Fatalf("empty action = %q", got)
	}
	if got := ActionFor(actionObs(false, 10), nil); got != ActionRepair {
		t.Fatalf("legacy scope action = %q", got)
	}
}

func TestRecurrenceOutranksCoverage(t *testing.T) {
	obs := actionObs(true, 10)
	obs.Coverage.After["unknown_evidence"] = 2
	if got := ActionFor(obs, &cadence.Scope{Version: 1, All: true}); got != ActionInspect {
		t.Fatalf("action = %q", got)
	}
}

func TestMissingAuditIsRepairableCoverage(t *testing.T) {
	obs := actionObs(false, 10)
	obs.Firing = mine.FiringActivity{Applicable: true, LogPresent: false}
	if got := ActionFor(obs, &cadence.Scope{Version: 1, All: true}); got != ActionRepair {
		t.Fatalf("action = %q", got)
	}
}

func TestActionSentenceNamesObservationLimits(t *testing.T) {
	obs := actionObs(true, 10)
	obs.Firing = mine.FiringActivity{Applicable: true, LogPresent: true}
	if got := ActionSentence(obs, &cadence.Scope{Version: 1, All: true}); got == "" {
		t.Fatal("empty recurrence sentence")
	}
}
