package mine

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// unresolved is a retry row that owes a ledger: no concerns, no resolver stamp.
func unresolved(ts int) gate.Row { return row(ts, "retry") }

// closedMiss is a retry row the resolver examined and closed as a permanent miss.
func closedMiss(ts int) gate.Row {
	r := row(ts, "retry")
	r["ledger_found"] = false
	r["backfilled"] = true
	return r
}

func TestChainEvidence(t *testing.T) {
	adjudicated := BuildChains([]gate.Row{
		row(1000, "deny"), concerns(row(1010, "retry"), "round", c("x")),
	}, false)[0]
	check(t, "an adjudicated chain is complete", ChainEvidence(adjudicated, 9_000_000), EvidenceComplete)

	owing := BuildChains([]gate.Row{row(1000, "deny"), unresolved(1010)}, false)[0]
	// The grace window is measured from the deny, not from the retry row.
	check(t, "inside the grace window it is pending",
		ChainEvidence(owing, 1000+gate.LedgerGrace-1), EvidencePending)
	check(t, "at the grace boundary it is missing",
		ChainEvidence(owing, 1000+gate.LedgerGrace), EvidenceMissing)

	missed := BuildChains([]gate.Row{row(2000, "deny"), closedMiss(2010)}, false)[0]
	check(t, "a closed miss is missing however young", ChainEvidence(missed, 2001), EvidenceMissing)

	// A cap row anchors on itself, and one with no finding slots owes nothing.
	capRow := row(3010, "cap")
	capRow["finding_slots"] = 0
	empty := BuildChains([]gate.Row{row(3000, "deny"), capRow}, false)[0]
	check(t, "a cap that showed no findings owes no ledger",
		ChainEvidence(empty, 9_000_000), EvidenceComplete)

	// The worst row wins: one complete round does not excuse an unresolved one.
	mixed := BuildChains([]gate.Row{
		row(4000, "deny"), concerns(row(4010, "retry"), "round", c("x")),
		firing(row(4020, "deny"), 2), closedMiss(4030),
	}, false)[0]
	check(t, "the worst row grades the chain", ChainEvidence(mixed, 9_000_000), EvidenceMissing)
}

func TestCoverEvidenceMovesUnmeasurableChainsToUnknown(t *testing.T) {
	all, _, _ := ParseScope(Record{"scope": map[string]any{"version": 1, "all": true}})
	good := BuildChains([]gate.Row{row(1000, "deny"), concerns(row(1010, "retry"), "round", c("x"))}, false)[0]
	miss := BuildChains([]gate.Row{row(2000, "deny"), closedMiss(2010)}, false)[0]
	young := BuildChains([]gate.Row{row(3000, "deny"), unresolved(3010)}, false)[0]

	cov := CoverEvidence([]Chain{good, miss, young}, all, 3050)
	check(t, "only the adjudicated chain stays eligible", cov.Eligible, 1)
	check(t, "both unmeasurable chains are unknown", cov.Unknown, 2)
	check(t, "both are unknown for want of evidence", cov.UnknownEvidence, 2)
	check(t, "the young one is pending", cov.Pending, 1)
	check(t, "no scope excluded anything", cov.UnknownScope, 0)
	check(t, "nothing is ineligible", cov.Ineligible, 0)

	// A zero clock skips the evidence pass, which is what a scope preview wants.
	preview := CoverEvidence([]Chain{good, miss, young}, all, 0)
	check(t, "a preview counts scope standing only", preview.Eligible, 3)
}

// scopedRecord is an installed record with an all-scope, splitting at ts.
func scopedRecord(ts int) Record {
	return Record{
		"id": "r", "class": "c", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": ts,
		"scope": map[string]any{"version": ScopeVersion, "all": true},
	}
}

// withClock runs fn with the package clock pinned, so a grace window is testable.
func withClock(at int64, fn func()) {
	prev := now
	now = func() int64 { return at }
	defer func() { now = prev }()
	fn()
}

func TestScoreRecordWithholdsOnMissingEvidence(t *testing.T) {
	var chains []Chain
	for i := 0; i < 6; i++ {
		ts := 1000 + i*10
		chains = append(chains, BuildChains([]gate.Row{
			row(ts, "deny"), concerns(row(ts+1, "retry"), "round", c("boom")),
		}, false)[0])
	}
	// Every after-window chain lost its adjudication for good.
	afterMiss := BuildChains([]gate.Row{row(5000, "deny"), closedMiss(5010)}, false)[0]
	afterYoung := BuildChains([]gate.Row{row(6000, "deny"), unresolved(6010)}, false)[0]

	withClock(9_000_000, func() {
		scored := scoreOK(t, scopedRecord(4000), append(append([]Chain{}, chains...), afterMiss), 0)
		check(t, "a permanently unadjudicated after-window withholds",
			scored["verdict"], "evidence-incomplete")
		cov := scored["after_coverage"].(map[string]any)
		check(t, "the lost chain is unknown, not a clean miss", cov["unknown"], 1)
		check(t, "and unknown for want of evidence", cov["unknown_evidence"], 1)
		check(t, "so nothing is eligible", cov["eligible"], 0)
	})

	// The same window, still inside the grace period, is only collecting.
	withClock(6000+gate.LedgerGrace-1, func() {
		scored := scoreOK(t, scopedRecord(4000), append(append([]Chain{}, chains...), afterYoung), 0)
		check(t, "a ledger that may still land is collecting", scored["verdict"], "collecting")
		check(t, "the young chain is pending",
			scored["after_coverage"].(map[string]any)["pending"], 1)
	})
}

func TestScoreRecordWithholdsOnAConfound(t *testing.T) {
	var before, after []Chain
	for i := 0; i < 6; i++ {
		ts := 1000 + i*10
		before = append(before, chain(t, ts, paths(model(row(ts, "deny"), "old-model"), "hooks/x.py")))
		ats := 5000 + i*10
		after = append(after, chain(t, ats, paths(model(row(ats, "deny"), "new-model"), "cli/x.go")))
	}
	withClock(9_000_000, func() {
		scored := scoreOK(t, scopedRecord(4000), append(before, after...), 0)
		check(t, "disjoint supports withhold the verdict", scored["verdict"], "confounded")
		reasons := scored["confounds"].([]Confound)
		if len(reasons) < 2 {
			t.Fatalf("both shifts are reported, got %#v", reasons)
		}
		check(t, "no soft warning duplicates a hard one", len(scored["composition"].([]Confound)), 0)
	})
}

func TestScoreRecordLegacyOutranksWithholding(t *testing.T) {
	legacy := scopedRecord(4000)
	delete(legacy, "scope")
	broken := BuildChains([]gate.Row{row(5000, "deny"), closedMiss(5010)}, false)[0]
	withClock(9_000_000, func() {
		scored := scoreOK(t, legacy, []Chain{broken}, 0)
		check(t, "an unscoped record reports its missing scope first",
			scored["verdict"], "legacy-unscoped")
	})
}

func TestEvidenceVerdictReadsCausesInOrderOfPermanence(t *testing.T) {
	empty := Coverage{Total: 1, Eligible: 1}
	cases := []struct {
		label string
		cov   Coverage
		want  string
	}{
		{"a closed miss is permanent",
			Coverage{Total: 1, Unknown: 1, UnknownEvidence: 1}, "evidence-incomplete"},
		{"metadata a scope needs and no row carried is permanent too",
			Coverage{Total: 1, Unknown: 1, UnknownScope: 1}, "evidence-incomplete"},
		{"a closed miss outranks a pending ledger beside it",
			Coverage{Total: 2, Unknown: 2, UnknownEvidence: 2, Pending: 1}, "evidence-incomplete"},
		{"an unknown scope outranks a pending ledger beside it",
			Coverage{Total: 2, Unknown: 2, UnknownScope: 1, UnknownEvidence: 1, Pending: 1},
			"evidence-incomplete"},
		{"only pending ledgers means the window measures itself",
			Coverage{Total: 1, Unknown: 1, UnknownEvidence: 1, Pending: 1}, "collecting"},
		{"an eligible chain withholds nothing", Coverage{Total: 1, Eligible: 1}, ""},
		{"an empty window withholds nothing", Coverage{}, ""},
	}
	for _, tc := range cases {
		check(t, "after window: "+tc.label, evidenceVerdict(empty, tc.cov), tc.want)
		check(t, "before window: "+tc.label, evidenceVerdict(tc.cov, empty), tc.want)
	}
}
