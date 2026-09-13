package cadence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

func scoredRow(ts int, text string) []gate.Row {
	deny := gate.Row{"ts": ts, "phase": "deny", "firing": 1, "files": 1}
	retry := gate.Row{"ts": ts + 10, "phase": "retry", "firing": 1, "files": 1,
		"ledger_join": "round", "session_id": "s1",
		"concerns": []any{map[string]any{"concern": text, "verdict": "ACCEPTED"}}}
	return []gate.Row{deny, retry}
}

func TestConvertersReadObservationAndCoverage(t *testing.T) {
	var rows []gate.Row
	for i := 0; i < 8; i++ {
		rows = append(rows, scoredRow(1000+i*100, "boom in the parser")...)
	}
	for i := 0; i < 8; i++ {
		rows = append(rows, scoredRow(9000+i*100, "unrelated finding")...)
	}
	record := mine.Record{
		"id": "r", "class": "c", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": 5000,
		"scope": map[string]any{"version": mine.ScopeVersion, "repo": "steer", "path": `\.go$`},
	}
	scored, err := mine.ScoreRecord(record, mine.EvidenceSet{Chains: mine.BuildChains(rows, false)})
	if err != nil {
		t.Fatal(err)
	}
	obs := observationFromJSON(scored["observation"])
	if obs.Coverage.Before["eligible"] == nil {
		t.Fatalf("observation lost coverage: %#v", obs)
	}
	if got := ScopeOf(scored["scope"]); got == nil || got.Repo != "steer" || got.Path != `\.go$` {
		t.Fatalf("scope did not survive conversion: %#v", got)
	}
	if got := CoverageOf(scored["before_coverage"]); got.Total == 0 || got.Total != got.Eligible+got.Ineligible+got.Unknown {
		t.Fatalf("coverage does not account for every chain: %+v", got)
	}
}

func TestQueueVersionAndObservationRoundTrip(t *testing.T) {
	iv := Intervention{
		ID: "r", Observation: mine.Observation{
			Recurrence: mine.Recurrence{Occurred: true, Matches: 2},
			Checks:     mine.CheckActivity{Applicable: true, Executions: 3, Blocked: 1},
		},
		Scope:          &Scope{Version: 1, All: true},
		BeforeCoverage: Coverage{Total: 8, Eligible: 8},
		AfterCoverage:  Coverage{Total: 6, Eligible: 6},
	}
	data, err := json.Marshal(Queue{Version: QueueVersion, Interventions: []Intervention{iv}})
	if err != nil {
		t.Fatal(err)
	}
	var back Queue
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Version != 7 || QueueVersion != 7 {
		t.Fatalf("queue version drifted: %d", back.Version)
	}
	if len(back.Interventions) != 1 || !back.Interventions[0].Observation.Recurrence.Occurred ||
		back.Interventions[0].Observation.Checks.Blocked != 1 {
		t.Fatalf("observation did not round-trip: %#v", back.Interventions)
	}
	for _, word := range []string{`"verdict"`, `"effect"`, `"required_after"`, `"confounds"`, `"composition"`} {
		if strings.Contains(string(data), word) {
			t.Fatalf("removed claim field %s remains in queue JSON: %s", word, data)
		}
	}
}

func observationFromJSON(v any) mine.Observation {
	data, _ := json.Marshal(v)
	var out mine.Observation
	_ = json.Unmarshal(data, &out)
	return out
}
