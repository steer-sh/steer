package mine

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// obsChain is one adjudicated chain: an opener, a round carrying the concerns, and
// the instrument the reviewer ran under.
func obsChain(t *testing.T, ts int, reviewer string, texts ...string) Chain {
	t.Helper()
	cs := make([]map[string]any, len(texts))
	for i, text := range texts {
		cs[i] = c(text)
	}
	rows := []gate.Row{
		instrument(row(ts, "deny"), reviewer),
		concerns(instrument(row(ts+1, "retry"), reviewer), "round", cs...),
	}
	built := BuildChains(rows, false)
	if len(built) != 1 {
		t.Fatalf("got %d chains, want 1", len(built))
	}
	return built[0]
}

// obsRecord is a scoped chain-corpus record installed at ts.
func obsRecord(tier, hookID string, ts int) Record {
	r := Record{
		"id": "obs", "class": "widget", "match": "widget", "tier": tier,
		"target": "check.sh", "status": "active", "installed_ts": ts,
		"corpus": CorpusReviewFindings, "unit": UnitChain,
		"scope": map[string]any{"version": ScopeVersion, "all": true},
	}
	if hookID != "" {
		r["hook_id"] = hookID
	}
	return r
}

func observationOf(t *testing.T, scored map[string]any) Observation {
	t.Helper()
	obs, ok := scored["observation"].(Observation)
	if !ok {
		t.Fatalf("the scored record carries no observation: %#v", scored["observation"])
	}
	return obs
}

// The chain observation reports the period, the counts, the activity bounds, the
// recurrence, the coverage and the identities — and claims nothing.
func TestChainObservationPinsEveryField(t *testing.T) {
	chains := []Chain{
		obsChain(t, 1000, "agy/1/aaa/v1", "widget parser drops a flag"),
		obsChain(t, 1100, "agy/1/aaa/v1", "unrelated nit"),
		obsChain(t, 3000, "agy/2/bbb/v1", "unrelated nit"),
		obsChain(t, 3100, "agy/2/bbb/v1", "widget parser drops a flag again"),
	}
	var obs Observation
	withClock(9000, func() {
		obs = observationOf(t, scoreOK(t, obsRecord("check", "", 2000), chains))
	})

	check(t, "the period opens at the install", obs.Counts.SplitTS, float64(2000))
	check(t, "the period closes at the read's clock", obs.Counts.ObservedTS, float64(9000))
	check(t, "the unit is the review chain", obs.Counts.Unit, UnitChain)
	check(t, "the before window's hits", obs.Counts.Before.Hits, 1)
	check(t, "the before window's eligible units", obs.Counts.Before.Eligible, 2)
	check(t, "the before window's first activity", obs.Counts.Before.FirstTS, float64(1000))
	check(t, "the before window's last activity", obs.Counts.Before.LastTS, float64(1101))
	check(t, "the after window's hits", obs.Counts.After.Hits, 1)
	check(t, "the after window's eligible units", obs.Counts.After.Eligible, 2)
	check(t, "the after window's first activity", obs.Counts.After.FirstTS, float64(3000))

	check(t, "the class recurred", obs.Recurrence.Occurred, true)
	check(t, "on one after-window chain", obs.Recurrence.Matches, 1)
	check(t, "the example is the newest match", obs.Recurrence.Example, "widget parser drops a flag again")
	check(t, "and it is an adjudicated finding", obs.Recurrence.ExampleKind, ExampleAdjudicatedFinding)

	check(t, "a check-tier record writes no audit rows", obs.Firing.Applicable, false)
	check(t, "so a zero is not a fact", obs.Firing.Known(), false)

	check(t, "the after coverage rides along", obs.Coverage.After["eligible"], 2)
	check(t, "the window held eligible work", obs.Coverage.ZeroEligibleAfter, false)

	check(t, "the identities are the reviewers", obs.Provenance.Kind, ProvenanceReviewer)
	check(t, "every before-window identity is listed", obs.Provenance.Before, []string{"agy/1/aaa/v1"})
	check(t, "every after-window identity is listed", obs.Provenance.After, []string{"agy/2/bbb/v1"})

	check(t, "the record installed a fix", obs.Declined, false)
}

// A window that saw no eligible work is absent opportunity, and the period bounds
// are the only pair that can say so: the activity bounds cannot express it.
func TestAQuietWindowIsToldApartFromAnEmptyOne(t *testing.T) {
	chains := []Chain{obsChain(t, 1000, "agy/1/aaa/v1", "widget parser drops a flag")}
	var obs Observation
	withClock(9000, func() {
		obs = observationOf(t, scoreOK(t, obsRecord("check", "", 2000), chains))
	})
	check(t, "no eligible unit after the install", obs.Counts.After.Eligible, 0)
	check(t, "which the flag names", obs.Coverage.ZeroEligibleAfter, true)
	check(t, "the activity bounds cannot express it", obs.Counts.After.FirstTS, float64(0))
	check(t, "the period still says how long it has been quiet", obs.Counts.ObservedTS, float64(9000))
	check(t, "and nothing recurred", obs.Recurrence.Occurred, false)
	check(t, "so no example is offered", obs.Recurrence.ExampleKind, "")
}

// A guard mined from review findings still writes audit rows. The count is
// window-level and says so: it cannot establish that the guard fired on this
// class's own eligible work.
func TestChainGuardActivityIsWindowLevelAndLabelled(t *testing.T) {
	chains := []Chain{
		obsChain(t, 1000, "agy/1/aaa/v1", "widget parser drops a flag"),
		obsChain(t, 3000, "agy/1/aaa/v1", "unrelated nit"),
	}
	firings := []Firing{
		// A block in a session that produced no commit: no chain can claim it.
		{HookID: "no-widget", Harness: "claude-code", SessionID: "never-committed", TS: 2500},
		// The same row read twice off two stores of a migrated machine.
		{HookID: "no-widget", Harness: "claude-code", SessionID: "s1", TS: 3050},
		{HookID: "no-widget", Harness: "claude-code", SessionID: "s1", TS: 3050},
		// Another harness at the same instant is its own firing.
		{HookID: "no-widget", Harness: "codex", SessionID: "s1", TS: 3050},
		// Outside the period, and another hook.
		{HookID: "no-widget", Harness: "claude-code", SessionID: "s0", TS: 1500},
		{HookID: "other-hook", Harness: "claude-code", SessionID: "s1", TS: 3060},
	}
	var obs Observation
	withClock(9000, func() {
		scored, err := ScoreRecord(obsRecord("guard", "no-widget", 2000),
			EvidenceSet{Chains: chains, Firings: firings, AuditPresent: true})
		if err != nil {
			t.Fatal(err)
		}
		obs = observationOf(t, scored)
	})
	check(t, "a guard-tier record has activity to show", obs.Firing.Applicable, true)
	check(t, "counted over the window period", obs.Firing.Scope, FiringScopeWindowPeriod)
	check(t, "never claimed as class-scoped", obs.Firing.ClassScoped, false)
	check(t, "one row per hook, harness and timestamp inside the period", obs.Firing.Rows, 3)
	check(t, "the newest firing", obs.Firing.NewestTS, float64(3050))
	check(t, "a window-level count answers no unit count", obs.Firing.Units, 0)
	check(t, "and a zero would be a fact", obs.Firing.Known(), true)
}

// A missing audit log is unknown activity, never a known zero.
func TestAMissingAuditLogIsUnknownRatherThanZero(t *testing.T) {
	chains := []Chain{obsChain(t, 1000, "agy/1/aaa/v1", "widget"), obsChain(t, 3000, "agy/1/aaa/v1", "widget")}
	withClock(9000, func() {
		scored, err := ScoreRecord(obsRecord("guard", "no-widget", 2000),
			EvidenceSet{Chains: chains, AuditPresent: false})
		if err != nil {
			t.Fatal(err)
		}
		obs := observationOf(t, scored)
		check(t, "the tier does write audit rows", obs.Firing.Applicable, true)
		check(t, "but no stream was readable", obs.Firing.LogPresent, false)
		check(t, "so the zero says nothing about the guard", obs.Firing.Known(), false)
		check(t, "and the row count is still zero", obs.Firing.Rows, 0)
	})
}

// A refused class is marked, so a reader phrasing its recurrence implies no fix.
func TestADeclinedRecordIsMarked(t *testing.T) {
	chains := []Chain{obsChain(t, 1000, "agy/1/aaa/v1", "widget"), obsChain(t, 3000, "agy/1/aaa/v1", "widget")}
	record := obsRecord("check", "", 0)
	record["status"] = "rejected"
	record["rejected_ts"] = 2000
	delete(record, "installed_ts")
	var obs Observation
	withClock(9000, func() {
		obs = observationOf(t, scoreOK(t, record, chains))
	})
	check(t, "the record installed no fix", obs.Declined, true)
	check(t, "the period opens at the refusal", obs.Counts.SplitTS, float64(2000))
	check(t, "and the recurrence is since the refusal", obs.Recurrence.Matches, 1)
}

// The session observation reports the class-scoped join, because a session is the
// unit and the rows can be tied to the eligible work.
func TestSessionObservationPinsEveryField(t *testing.T) {
	units := append(window(1000, 2, "claude-code", "proj", "bare python here"),
		window(3000, 2, "claude-code", "proj", "bare python here")...)
	units[0].Instrument = "scan-aaa/claude-code:1"
	units[2].Instrument = "scan-aaa/claude-code:2"
	r := sessionRecord(t, "bare python", "guard", "no-bare-python", 2000)
	firings := []Firing{
		{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3000", TS: 3000},
		{HookID: "no-bare-python", Harness: "claude-code", SessionID: "s3001", TS: 3001},
	}
	var obs Observation
	withClock(9000, func() {
		obs = observationOf(t, scoreSession(t, r,
			EvidenceSet{Sessions: units, Firings: firings, AuditPresent: true}))
	})

	check(t, "the unit is the session", obs.Counts.Unit, UnitSession)
	check(t, "the before window's hits", obs.Counts.Before.Hits, 2)
	check(t, "the after window's eligible units", obs.Counts.After.Eligible, 2)
	check(t, "the after window's first activity", obs.Counts.After.FirstTS, float64(3000))

	check(t, "the class recurred", obs.Recurrence.Occurred, true)
	check(t, "the example is a cluster key", obs.Recurrence.ExampleKind, ExampleClusterKey)
	check(t, "not an adjudicated finding", obs.Recurrence.Example, "bare python here")

	check(t, "the join is class-scoped", obs.Firing.ClassScoped, true)
	check(t, "over the window's eligible units", obs.Firing.Scope, FiringScopeEligibleUnits)
	check(t, "the rows it joined", obs.Firing.Rows, 2)
	check(t, "the units they landed in", obs.Firing.Units, 2)
	check(t, "the newest of them", obs.Firing.NewestTS, float64(3001))

	check(t, "the identities are the parsers", obs.Provenance.Kind, ProvenanceParser)
	check(t, "every after-window identity is listed", obs.Provenance.After, []string{"scan-aaa/claude-code:2"})
}

// A record with no frozen scope counted every unit as eligible, so its window was
// never defined. The scored line has to say that rather than read as settled.
func TestTheScoredLineNamesAMissingScopeRatherThanAQuietWindow(t *testing.T) {
	quiet := Observation{}
	check(t, "a scoped record with nothing to show", observationState(quiet, true), "quiet")
	check(t, "an unscoped one names the gap", observationState(quiet, false), "no-scope")

	// A recurrence is an observed fact whatever the scope says, and outranks it —
	// the order status.ActionFor keeps.
	recurred := Observation{Recurrence: Recurrence{Occurred: true}}
	check(t, "recurrence outranks the scope", observationState(recurred, false), "recurred")
}

// A ledger cause is never a gap: a pending row settles, an overdue one is the
// resolver's own status entry, and a closed miss has no repair.
func TestALedgerCauseIsNeverACoverageGap(t *testing.T) {
	settling := map[string]any{"unknown": 3, "unknown_evidence": 3, "pending": 3}
	if CoverageGap(settling) {
		t.Error("a window whose unknowns are all still settling is no gap")
	}
	missed := map[string]any{"unknown": 4, "unknown_evidence": 4, "pending": 1, "ledger_missed": 2, "ledger_overdue": 1}
	if CoverageGap(missed) {
		t.Error("a closed or overdue ledger is disclosed, not a gap")
	}
	obs := Observation{Coverage: ObservationCoverage{After: missed}}
	if MissingCoverage(obs) {
		t.Error("a window holding only ledger causes asks for no repair")
	}
	if !EvidenceExcluded(obs) {
		t.Error("the excluded chains are still disclosed")
	}
	var out strings.Builder
	writeObservation(&out, obs)
	if !strings.Contains(out.String(), "0 before and 4 after carry no adjudication") {
		t.Errorf("the scored line hides the excluded chains:\n%s", out.String())
	}
	if !CoverageGap(map[string]any{"unknown": 2, "unknown_scope": 2}) {
		t.Error("metadata no row carried is a gap")
	}
	// A session map writes neither name, so its whole bucket is the remainder.
	if !CoverageGap(map[string]any{"unknown": 5}) {
		t.Error("a session window that could not be placed is a gap")
	}
	// An audit log a guard's activity cannot be read without is the fourth cause.
	if !MissingCoverage(Observation{Firing: FiringActivity{Applicable: true}}) {
		t.Error("an unreadable audit log is missing coverage")
	}
	if MissingCoverage(Observation{Firing: FiringActivity{Applicable: true, LogPresent: true}}) {
		t.Error("a present log recording no block is a reading, not a gap")
	}
}
