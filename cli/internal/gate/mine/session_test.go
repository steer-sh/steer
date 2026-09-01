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

func scoreSession(t *testing.T, r Record, ev EvidenceSet, minAfter int) map[string]any {
	t.Helper()
	res, err := ScoreSessionRecord(r, ev, minAfter)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A guard's win is only its own when the audit log says it fired inside the window.
func TestSessionEffectiveNeedsAJoinedFiring(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)

	silent := scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: true}, 12)
	check(t, "a fall the guard never caused", silent["verdict"], "unattributed")

	firings := []Firing{{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3000", TS: 3000}}
	joined := scoreSession(t, r, EvidenceSet{Sessions: units, Firings: firings, AuditPresent: true}, 12)
	check(t, "a fall the guard caused", joined["verdict"], "effective")
	attr := joined["attribution"].(Attribution)
	check(t, "the join counts rows", attr.Firings, 1)
	check(t, "the join counts sessions", attr.Sessions, 1)

	// No readable audit stream is missing evidence, not a silent guard.
	blind := scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: false}, 12)
	check(t, "no audit stream withholds", blind["verdict"], "evidence-incomplete")
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
	res := scoreSession(t, r, EvidenceSet{Sessions: units, Firings: wrong, AuditPresent: true}, 12)
	check(t, "no row joins", res["attribution"].(Attribution).Firings, 0)
	check(t, "so the fall stays unattributed", res["verdict"], "unattributed")
}

// A class that persisted while the guard stayed silent is a mis-aimed matcher, which
// is a different repair from a guard that fired and did not help.
func TestSessionClassPersistingWithNoFiringIsNotTriggered(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "bare python here")...)
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units, AuditPresent: true}, 12)
	check(t, "a silent guard over a live class", res["verdict"], "not-triggered")

	firings := []Firing{{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3005"}}
	fired := scoreSession(t, r, EvidenceSet{Sessions: units, Firings: firings, AuditPresent: true}, 12)
	check(t, "a firing guard over a live class", fired["verdict"], "ineffective")
}

// A tier that writes no audit rows reads the range alone.
func TestSessionNonGuardTierKeepsTheRangeVerdict(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units}, 12)
	check(t, "attribution does not apply", res["attribution"].(Attribution).Applicable, false)
	check(t, "the range stands alone", res["verdict"], "effective")
}

// A thin after-window is collecting, whatever the point estimate says.
func TestSessionThinAfterWindowIsCollecting(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 2, "claude-code", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units}, 12)
	check(t, "too few sessions to decide", res["verdict"], "collecting")
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
	res := scoreSession(t, r, EvidenceSet{Sessions: units}, 12)
	before := res["before_coverage"].(map[string]any)
	after := res["after_coverage"].(map[string]any)
	check(t, "the straddling session counts once before", before["partial_exposure"], 1)
	check(t, "and once after", after["partial_exposure"], 1)
	check(t, "it is not eligible", before["eligible"], 12)
	check(t, "every session is accounted for", before["total"], 13)

	// A window emptied only by partial exposure fills on its own, so it is collecting.
	only := []SessionUnit{spans(friction(1900, "claude-code", "proj", "bare python here"), 2100)}
	thin := scoreSession(t, sessionRecord(t, "bare python", "check", "", 2000),
		EvidenceSet{Sessions: only}, 1)
	check(t, "no eligible session either side", thin["verdict"], "collecting")
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
	res := scoreSession(t, r, EvidenceSet{Sessions: units}, 12)
	before := res["before_coverage"].(map[string]any)
	check(t, "only the scoped harness counts", before["eligible"], 12)
	check(t, "the other harness is ineligible", before["ineligible"], 6)
	check(t, "the elimination is measured on codex alone", res["verdict"], "effective")

	unstamped := []SessionUnit{friction(1000, "", "proj", "bare python here")}
	unknown := scoreSession(t, r, EvidenceSet{Sessions: unstamped}, 1)
	check(t, "an unstamped session cannot be decided", unknown["verdict"], "evidence-incomplete")
}

// Two windows sharing no harness measure two different populations.
func TestSessionDisjointHarnessIsAConfound(t *testing.T) {
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(3000, 12, "codex", "proj", "nothing here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	res := scoreSession(t, r, EvidenceSet{Sessions: units}, 12)
	check(t, "a swapped harness withholds", res["verdict"], "confounded")
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
