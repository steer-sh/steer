package status

import (
	"strings"
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
	if got := ActionFor(actionObs(true, 10), scope, "instruction", nil); got != ActionInspect {
		t.Fatalf("recurrence action = %q", got)
	}
	if got := ActionFor(actionObs(false, 10), scope, "instruction", nil); got != ActionObserve {
		t.Fatalf("quiet action = %q", got)
	}
	if got := ActionFor(actionObs(false, 0), scope, "instruction", nil); got != ActionObserve {
		t.Fatalf("empty action = %q", got)
	}
	if got := ActionFor(actionObs(false, 10), nil, "instruction", nil); got != ActionRepair {
		t.Fatalf("legacy scope action = %q", got)
	}
}

func TestRecurrenceOutranksCoverage(t *testing.T) {
	obs := actionObs(true, 10)
	obs.Coverage.After["unknown_evidence"] = 2
	if got := ActionFor(obs, &cadence.Scope{Version: 1, All: true}, "instruction", nil); got != ActionInspect {
		t.Fatalf("action = %q", got)
	}
}

func TestMissingAuditIsRepairableCoverage(t *testing.T) {
	obs := actionObs(false, 10)
	obs.Firing = mine.FiringActivity{Applicable: true, LogPresent: false}
	if got := ActionFor(obs, &cadence.Scope{Version: 1, All: true}, "instruction", nil); got != ActionRepair {
		t.Fatalf("action = %q", got)
	}
}

func TestActionSentenceNamesObservationLimits(t *testing.T) {
	obs := actionObs(true, 10)
	obs.Firing = mine.FiringActivity{Applicable: true, LogPresent: true}
	if got := ActionSentence(obs, &cadence.Scope{Version: 1, All: true}, "instruction", nil); got == "" {
		t.Fatal("empty recurrence sentence")
	}
}

// Stopping the tracking removes no fix, so the phrase names the command that does,
// for the tier the record binds, and never claims the fix is still in place.
func TestRemovalPhraseNamesEachTiersRemovalCommand(t *testing.T) {
	cases := []struct {
		tier string
		a    *cadence.Artifact
		want string
	}{
		{"guard", &cadence.Artifact{ID: "no-bare-python", Scope: "global"},
			"stopping the tracking ends the measurement and removes no guard; to remove it, run: steer remove no-bare-python --scope global"},
		{"prose", &cadence.Artifact{ID: "config-path", Scope: "project"},
			"stopping the tracking ends the measurement and removes no rule; to remove it, run: steer rules remove config-path --scope project"},
		{"review", &cadence.Artifact{ID: "fp-sort", Scope: "global"},
			"stopping the tracking ends the measurement and removes no review prompt entry; to remove it, run: steer commit-gate addendum remove fp-sort --scope global"},
		{"check", &cadence.Artifact{ID: "no-count", Repo: "/w/steer"},
			"stopping the tracking ends the measurement and removes no check; to remove it, run: steer check remove no-count"},
		{"check", &cadence.Artifact{State: cadence.ArtifactUnbound},
			"stopping the tracking ends the measurement and removes no check"},
		{"check", nil, "stopping the tracking ends the measurement and removes nothing"},
	}
	for _, c := range cases {
		if got := RemovalPhrase(c.tier, c.a); got != c.want {
			t.Errorf("RemovalPhrase(%s, %+v)\n got %q\nwant %q", c.tier, c.a, got, c.want)
		}
	}
}

// Each artifact state reads as its own sentence, and a project review entry that
// left HEAD says the commit gate no longer reads it.
func TestArtifactPhraseNamesTheState(t *testing.T) {
	cases := []struct {
		tier string
		a    *cadence.Artifact
		want string
	}{
		{"instruction", &cadence.Artifact{ID: "config-path", Scope: "global", State: cadence.ArtifactAbsent},
			"the rule config-path is no longer installed (global)"},
		{"review", &cadence.Artifact{ID: "fp-sort", Scope: "project", State: cadence.ArtifactAbsent},
			"the review prompt entry fp-sort is not in the project's committed review prompt, so the commit gate does not read it"},
		{"check", &cadence.Artifact{ID: "no-count", Repo: "/w/steer", State: cadence.ArtifactChanged},
			"the check no-count is declared, but its detector differs from the one this record registered"},
		{"guard", &cadence.Artifact{ID: "g", Scope: "project", Repo: "/w/other", State: cadence.ArtifactElsewhere},
			"the guard g belongs to /w/other, which this reading does not inspect"},
		{"guard", &cadence.Artifact{ID: "g", Scope: "global", State: cadence.ArtifactPresent, Removed: "2026-02-01T00:00:00.000000000Z"},
			"the guard g is in place as recorded; the archive records a removal of it at 2026-02-01T00:00:00.000000000Z, " +
				"after this record was registered, so check whether the guard was out of place during this record's window"},
		{"check", nil, ""},
	}
	for _, c := range cases {
		if got := ArtifactPhrase(c.tier, c.a); got != c.want {
			t.Errorf("ArtifactPhrase(%s, %+v)\n got %q\nwant %q", c.tier, c.a, got, c.want)
		}
	}
}

// A reworded rule asks for a repair and names the re-baseline command. A check
// whose detector was edited does not: its digest moves with the test it runs.
func TestADriftedFixAsksForANewBaseline(t *testing.T) {
	scope := &cadence.Scope{Version: 1, All: true}
	obs := actionObs(false, 10)
	drifted := &cadence.Artifact{ID: "my-fix", State: cadence.ArtifactChanged}

	if got := ActionFor(obs, scope, "instruction", drifted); got != ActionRepair {
		t.Errorf("reworded rule action = %q, want %q", got, ActionRepair)
	}
	if got := ActionFor(obs, scope, "check", drifted); got != ActionObserve {
		t.Errorf("edited detector action = %q, want %q", got, ActionObserve)
	}
	if got := ActionFor(obs, scope, "instruction", &cadence.Artifact{ID: "my-fix", State: cadence.ArtifactPresent}); got != ActionObserve {
		t.Errorf("present artifact action = %q, want %q", got, ActionObserve)
	}
	sentence := ActionSentence(obs, scope, "instruction", drifted)
	if !strings.Contains(sentence, "steer fix register --supersedes") {
		t.Errorf("the sentence names no re-baseline command:\n%s", sentence)
	}
	if !strings.Contains(sentence, "earlier text") {
		t.Errorf("the sentence does not say which text the counts describe:\n%s", sentence)
	}
	if got := ActionSentence(obs, scope, "check", drifted); strings.Contains(got, "--supersedes") {
		t.Errorf("an edited detector must not be sent to re-baseline:\n%s", got)
	}
}

// An observed escape is a fact about the work. Filing it under a repair to the
// record's baseline hides it, so recurrence outranks drift.
func TestRecurrenceOutranksDrift(t *testing.T) {
	drifted := &cadence.Artifact{ID: "my-fix", State: cadence.ArtifactChanged}
	got := ActionFor(actionObs(true, 10), &cadence.Scope{Version: 1, All: true}, "instruction", drifted)
	if got != ActionInspect {
		t.Fatalf("action = %q, want %q", got, ActionInspect)
	}
}
