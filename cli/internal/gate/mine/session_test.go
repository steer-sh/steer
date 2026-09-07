package mine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// friction is one normalized root session carrying correction signals.
func friction(ts int, harness, project string, texts ...string) SessionUnit {
	return frictionKind(ts, harness, project, SignalCorrection, texts...)
}

func frictionKind(ts int, harness, project, kind string, texts ...string) SessionUnit {
	u := SessionUnit{
		Harness: harness, SessionID: fmt.Sprintf("s%d", ts), Project: project,
		StartTS: float64(ts), EndTS: float64(ts),
	}
	for _, text := range texts {
		u.Signals = append(u.Signals, SessionSignal{Kind: kind, Text: text, TS: float64(ts)})
	}
	return u
}

// spans makes a session that started before ts and ended after it.
func spans(u SessionUnit, end int) SessionUnit {
	u.EndTS = float64(end)
	return u
}

// sessionRecord is one registered transcript-friction class, scoped to everything.
func sessionRecord(t *testing.T, match, tier, hookID string, installed int) Record {
	t.Helper()
	scope, err := BuildScope(true, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := Record{
		"id": "friction", "class": "friction", "match": match,
		"corpus": CorpusTranscript, "unit": UnitSession,
		"tier": tier, "target": "hooks/friction.py", "status": "active",
		"installed_ts": installed, "installed_date": stamp(float64(installed)),
		"scope": scope,
	}
	if hookID != "" {
		r["hook_id"] = hookID
	}
	return r
}

// window builds n sessions starting at ts, each carrying the given signal texts.
func window(start, n int, harness, project string, texts ...string) []SessionUnit {
	var out []SessionUnit
	for i := 0; i < n; i++ {
		out = append(out, friction(start+i, harness, project, texts...))
	}
	return out
}

func scoreSession(t *testing.T, r Record, ev EvidenceSet) map[string]any {
	t.Helper()
	res, err := ScoreSessionRecord(r, ev)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// observed reads the observation off a scored record, which is where every field
// count now lives.
func observed(t *testing.T, res map[string]any) Observation {
	t.Helper()
	obs, ok := res["observation"].(Observation)
	if !ok {
		t.Fatal("a scored session record must carry an observation")
	}
	return obs
}

// Guard activity rides beside the counts and is never folded into them. A quiet
// window with no firing and a quiet window with one read the same on the counts and
// differ on the firing block, which is the whole point of reporting both.
func TestSessionFiringActivityRidesBesideTheCounts(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)

	silent := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: true}))
	check(t, "the class did not recur", silent.Recurrence.Occurred, false)
	check(t, "the guard recorded nothing", silent.Firing.Rows, 0)
	check(t, "and that zero is known", silent.Firing.Known(), true)
	check(t, "the join is class-scoped", silent.Firing.ClassScoped, true)

	firings := []Firing{{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3000", TS: 3000}}
	joined := scoreSession(t, r, EvidenceSet{Sessions: units, Firings: firings, AuditPresent: true})
	check(t, "the join counts rows", observed(t, joined).Firing.Rows, 1)
	check(t, "the join counts sessions", observed(t, joined).Firing.Units, 1)

	// No readable audit stream is unknown activity, not a silent guard.
	blind := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: false}))
	check(t, "an absent log is not a zero", blind.Firing.Known(), false)
}

// The join is exact: a firing from another harness, another hook or another session is
// not this window's evidence.
func TestSessionFiringJoinIsExact(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)
	wrong := []Firing{
		{HookID: "some-other-hook", Harness: "claude-code", SessionID: "s3000"},
		{HookID: "no-bare-python", Harness: "codex", SessionID: "s3000"},
		{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s1000"},
		{HookID: "no-bare-python", Harness: "claude-code", SessionID: "unknown"},
	}
	res := scoreSession(t, r, EvidenceSet{Sessions: units, Firings: wrong, AuditPresent: true})
	check(t, "no row joins", observed(t, res).Firing.Rows, 0)
	check(t, "and the zero is a known one", observed(t, res).Firing.Known(), true)
}

// A recurrence and the guard activity are two separate readings. A class that kept
// matching while the guard recorded nothing reads differently from one that kept
// matching while the guard blocked, and neither reading is an efficacy claim.
func TestSessionRecurrenceIsReportedBesideFiring(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "bare python here")...)
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)
	silent := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: true}))
	check(t, "the class recurred", silent.Recurrence.Occurred, true)
	check(t, "the guard recorded nothing", silent.Firing.Rows, 0)
	check(t, "the example is a cluster key", silent.Recurrence.ExampleKind, ExampleClusterKey)

	firings := []Firing{{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3005"}}
	fired := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units, Firings: firings, AuditPresent: true}))
	check(t, "the class still recurred", fired.Recurrence.Occurred, true)
	check(t, "and the guard blocked once", fired.Firing.Rows, 1)
}

// A tier that writes no audit rows carries the counts with no firing block at all,
// so a reader never mistakes "writes none" for "recorded none".
func TestSessionNonGuardTierReportsNoFiring(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	obs := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units}))
	check(t, "firing does not apply", obs.Firing.Applicable, false)
	check(t, "the before-window carries the hits", obs.Counts.Before.Hits, 12)
	check(t, "the after-window carries none", obs.Counts.After.Hits, 0)
	check(t, "over its own eligible sessions", obs.Counts.After.Eligible, 12)
}

// A thin after-window reports its own size rather than a threshold. The reader sees
// two sessions and decides what that is worth.
func TestSessionThinAfterWindowReportsItsSize(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 2, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	obs := observed(t, scoreSession(t, r, EvidenceSet{Sessions: units}))
	check(t, "the after-window names its eligible sessions", obs.Counts.After.Eligible, 2)
	check(t, "and its hits", obs.Counts.After.Hits, 0)
}

// Partial exposure is its own coverage category: a session that straddles the install
// ran under both regimes, so it is evidence about neither window.
func TestSessionPartialExposureIsItsOwnCategory(t *testing.T) {
	units := []SessionUnit{
		spans(friction(1900, "claude-code", "proj", "bare python here"), 2100),
	}
	units = append(units, window(1000, 12, "claude-code", "proj", "bare python here")...)
	units = append(units, window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units})
	before := res["before_coverage"].(map[string]any)
	after := res["after_coverage"].(map[string]any)
	check(t, "the straddling session counts once before", before["partial_exposure"], 1)
	check(t, "and once after", after["partial_exposure"], 1)
	check(t, "it is not eligible", before["eligible"], 12)
	check(t, "every session is accounted for", before["total"], 13)

	// A window emptied only by partial exposure holds no eligible session, which the
	// coverage says outright rather than through a withheld verdict.
	only := []SessionUnit{spans(friction(1900, "claude-code", "proj", "bare python here"), 2100)}
	thin := observed(t, scoreSession(t, sessionRecord(t, "bare python", "check", "", 2000),
		EvidenceSet{Sessions: only}))
	check(t, "no eligible session either side", thin.Counts.After.Eligible, 0)
	check(t, "and the flag says the after-window is empty", thin.Coverage.ZeroEligibleAfter, true)
	check(t, "the straddling session is counted as partly exposed", thin.Coverage.After["partial_exposure"], 1)
}

// A harness scope freezes the denominator: a session from another harness is
// ineligible, and one with no harness stamp is unknown rather than a shift.
func TestSessionHarnessScopeSelectsTheDenominator(t *testing.T) {
	scope, err := BuildScope(false, []string{"codex"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := sessionRecord(t, "bare python", "check", "", 2000)
	r["scope"] = scope
	units := append(window(1000, 12, "codex", "proj", "bare python here"),
		window(1100, 6, "claude-code", "proj", "bare python here")...)
	units = append(units, window(3000, 12, "codex", "proj", "nothing here")...)
	res := scoreSession(t, r, EvidenceSet{Sessions: units})
	before := res["before_coverage"].(map[string]any)
	check(t, "only the scoped harness counts", before["eligible"], 12)
	check(t, "the other harness is ineligible", before["ineligible"], 6)
	check(t, "the counts run over codex alone", observed(t, res).Counts.Before.Eligible, 12)

	unstamped := []SessionUnit{friction(1000, "", "proj", "bare python here")}
	unknown := observed(t, scoreSession(t, r, EvidenceSet{Sessions: unstamped}))
	check(t, "an unstamped session is unknown, not ineligible", unknown.Coverage.Before["unknown"], 1)
	check(t, "so it enters no count", unknown.Counts.Before.Eligible, 0)
}

// Two windows sharing no harness measure two different populations, and each window
// reports the harnesses it held so a reader can see that for themselves.
func TestSessionHarnessesAreReportedPerWindow(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "codex", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units})
	checkStrings(t, "the before-window names its harness", res["before_hosts"], []string{"claude-code"})
	checkStrings(t, "the after-window names another", res["after_hosts"], []string{"codex"})
}

// checkStrings compares a scored record's string list against what the window held.
func checkStrings(t *testing.T, what string, got any, want []string) {
	t.Helper()
	list, _ := got.([]string)
	if fmt.Sprint(list) != fmt.Sprint(want) {
		t.Errorf("%s: got %v, want %v", what, list, want)
	}
}

// The two scopes share one store on a migrated machine, so a row read twice is one
// firing.
func TestLoadFiringsDeduplicatesAcrossStores(t *testing.T) {
	dir := t.TempDir()
	line := `{"hook_name":"no-bare-python","harness":"claude-code","session_id":"s1",` +
		`"timestamp":"2026-08-01T10:00:00Z"}`
	other := `{"hook_name":"no-bare-python","harness":"claude-code","session_id":"s2",` +
		`"timestamp":"2026-08-01T11:00:00Z"}`
	body := line + "\n" + other + "\n" + line + "\nnot json\n\n"
	if err := os.WriteFile(filepath.Join(dir, AuditLogName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	firings, present := LoadFirings(dir, dir, "", filepath.Join(dir, "absent"))
	check(t, "a store was readable", present, true)
	check(t, "one row per distinct decision", len(firings), 2)
	check(t, "rows are ordered by time", firings[0].SessionID, "s1")

	none, absent := LoadFirings(filepath.Join(dir, "absent"))
	check(t, "no store is not an empty store", absent, false)
	check(t, "and yields no firings", len(none), 0)
}
