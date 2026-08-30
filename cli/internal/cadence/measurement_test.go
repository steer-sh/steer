package cadence

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// scoredRow is a review round whose adjudication landed, carrying the class text.
func scoredRow(ts int, text string) []gate.Row {
	deny := gate.Row{"ts": ts, "phase": "deny", "firing": 1, "files": 1}
	retry := gate.Row{"ts": ts + 10, "phase": "retry", "firing": 1, "files": 1,
		"ledger_join": "round", "session_id": "s1",
		"concerns": []any{map[string]any{"concern": text, "verdict": "ACCEPTED"}}}
	return []gate.Row{deny, retry}
}

// TestConvertersReadTheScorersOwnKeys wires the real mine.ScoreRecord output through
// the queue converters. A renamed key in the scorer fails here rather than silently
// emitting a zeroed contract to every render target.
func TestConvertersReadTheScorersOwnKeys(t *testing.T) {
	var rows []gate.Row
	for i := 0; i < 8; i++ { // before-window: the class fires on every chain
		rows = append(rows, scoredRow(1000+i*100, "boom in the parser")...)
	}
	for i := 0; i < 8; i++ { // after-window: it never fires again
		rows = append(rows, scoredRow(9000+i*100, "unrelated finding")...)
	}
	record := mine.Record{
		"id": "r", "class": "c", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": 5000,
		"scope": map[string]any{"version": mine.ScopeVersion, "repo": "steer", "path": `\.go$`},
	}
	scored, err := mine.ScoreRecord(record, mine.BuildChains(rows, false), 0)
	if err != nil {
		t.Fatal(err)
	}

	sc := ScopeOf(scored["scope"])
	if sc == nil || sc.Version != mine.ScopeVersion || sc.Repo != "steer" || sc.Path != `\.go$` {
		t.Errorf("scope did not survive the conversion: %#v", sc)
	}
	before := CoverageOf(scored["before_coverage"])
	after := CoverageOf(scored["after_coverage"])
	if before.Total == 0 || after.Total == 0 {
		t.Errorf("coverage totals are empty: before %+v, after %+v", before, after)
	}
	if before.Total != before.Eligible+before.Ineligible+before.Unknown {
		t.Errorf("coverage does not account for every chain: %+v", before)
	}
	e := EffectOf(scored["effect"])
	if e.Method == "" || e.Confidence == 0 {
		t.Errorf("effect lost its method or confidence: %+v", e)
	}
	if e.Upper != nil && math.IsInf(*e.Upper, 1) {
		t.Error("an infinite upper bound must arrive as null, never as +Inf")
	}
	if ConfoundsOf(scored["confounds"]) == nil || ConfoundsOf(scored["composition"]) == nil {
		t.Error("confound lists must be empty, never nil")
	}
	// required_after is null exactly when the scorer says no window could decide it.
	if got, want := RequiredAfterOf(scored["required_after"]) == nil, scored["required_after"] == nil; got != want {
		t.Errorf("required_after nullness disagrees with the scorer: %v vs %v", got, want)
	}
}

// TestQueueVersionRidesTheMeasurementContract pins that the queue a consumer reads
// declares the version whose record shape it actually carries.
func TestQueueVersionRidesTheMeasurementContract(t *testing.T) {
	iv := Intervention{
		ID: "r", Verdict: "confounded",
		Scope:          &Scope{Version: 1, All: true},
		BeforeCoverage: Coverage{Total: 8, Eligible: 8},
		AfterCoverage:  Coverage{Total: 6, Eligible: 6},
		Effect:         Effect{Lower: 0.2, Confidence: 0.9, Method: "clopper-pearson / mixture-cs"},
		Confounds:      []mine.Confound{{Category: "instrument", Reason: "drifted"}},
		Composition:    []mine.Confound{},
	}
	data, err := json.Marshal(Queue{Version: QueueVersion, Interventions: []Intervention{iv}})
	if err != nil {
		t.Fatal(err)
	}
	var back Queue
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Version != QueueVersion {
		t.Errorf("queue version drifted: %d", back.Version)
	}
	if len(back.Interventions) != 1 {
		t.Fatalf("want one intervention, got %d", len(back.Interventions))
	}
	got := back.Interventions[0]
	if got.Scope == nil || !got.Scope.All {
		t.Errorf("scope did not round-trip: %#v", got.Scope)
	}
	if got.RequiredAfter != nil {
		t.Errorf("an absent required_after must decode as nil, got %v", *got.RequiredAfter)
	}
	if got.Effect.Upper != nil {
		t.Errorf("an unbounded upper must decode as nil, got %v", *got.Effect.Upper)
	}
	if len(got.Confounds) != 1 || got.Confounds[0].Category != "instrument" {
		t.Errorf("confounds did not round-trip: %#v", got.Confounds)
	}
}

// TestConfoundsOfCarriesAPopulatedList exercises the conversion with real confounds
// on it. An empty list converts identically whatever the scorer's element type is, so
// only a populated one proves the assertion still matches what ScoreRecord emits.
func TestConfoundsOfCarriesAPopulatedList(t *testing.T) {
	var rows []gate.Row
	for i := 0; i < 6; i++ {
		before := scoredRow(1000+i*100, "boom in the parser")
		after := scoredRow(9000+i*100, "boom in the parser")
		for _, r := range after {
			r["instrument"] = "new-reviewer"
		}
		rows = append(rows, append(before, after...)...)
	}
	record := mine.Record{
		"id": "r", "class": "c", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": 5000, "instrument": "old-reviewer",
		"scope": map[string]any{"version": mine.ScopeVersion, "all": true},
	}
	scored, err := mine.ScoreRecord(record, mine.BuildChains(rows, false), 0)
	if err != nil {
		t.Fatal(err)
	}
	if scored["verdict"] != "confounded" {
		t.Fatalf("a changed instrument withholds the verdict, got %v", scored["verdict"])
	}
	got := ConfoundsOf(scored["confounds"])
	if len(got) == 0 {
		t.Fatal("the confound list arrived empty — the converter no longer matches the scorer")
	}
	if got[0].Category == "" || got[0].Reason == "" {
		t.Errorf("a confound lost its category or reason: %#v", got[0])
	}
}
