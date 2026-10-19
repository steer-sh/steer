package mine

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func unresolved(ts int) gate.Row { return row(ts, "retry") }

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
	check(t, "inside the grace window it is pending", ChainEvidence(owing, 1000+gate.LedgerGrace-1), EvidencePending)
	check(t, "at the grace boundary it is overdue", ChainEvidence(owing, 1000+gate.LedgerGrace), EvidenceOverdue)

	missed := BuildChains([]gate.Row{row(2000, "deny"), closedMiss(2010)}, false)[0]
	check(t, "a closed miss is missed however young", ChainEvidence(missed, 2001), EvidenceMissed)

	capRow := row(3010, "cap")
	capRow["finding_slots"] = 0
	empty := BuildChains([]gate.Row{row(3000, "deny"), capRow}, false)[0]
	check(t, "a cap that showed no findings owes no ledger", ChainEvidence(empty, 9_000_000), EvidenceComplete)

	prose := row(3510, "cap")
	prose["finding_slots"] = 0
	prose["reviews_off_format"] = 2
	offFormat := BuildChains([]gate.Row{row(3500, "deny"), prose}, false)[0]
	check(t, "an off-format cap still owes a ledger", ChainEvidence(offFormat, 9_000_000), EvidenceOverdue)

	mixed := BuildChains([]gate.Row{
		row(4000, "deny"), concerns(row(4010, "retry"), "round", c("x")),
		firing(row(4020, "deny"), 2), closedMiss(4030),
	}, false)[0]
	check(t, "the worst row grades the chain", ChainEvidence(mixed, 9_000_000), EvidenceMissed)

	stampedOnly := row(5010, "retry")
	stampedOnly["ledger_found"] = false
	reopened := BuildChains([]gate.Row{row(5000, "deny"), stampedOnly}, false)[0]
	check(t, "a stamp the resolver has not closed is overdue however young", ChainEvidence(reopened, 5001), EvidenceOverdue)
}

func TestAnOverdueRowOutranksAClosedMissInOneChain(t *testing.T) {
	both := BuildChains([]gate.Row{
		row(1000, "deny"), closedMiss(1010),
		firing(row(1020, "deny"), 2), unresolved(1030),
	}, false)[0]
	check(t, "overdue work is not hidden behind a miss", ChainEvidence(both, 9_000_000), EvidenceOverdue)

	young := BuildChains([]gate.Row{
		row(2000, "deny"), closedMiss(2010),
		firing(row(2020, "deny"), 2), unresolved(2030),
	}, false)[0]
	check(t, "a miss is not described as settling", ChainEvidence(young, 2031), EvidenceMissed)
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
	check(t, "the closed one is a miss", cov.LedgerMissed, 1)
	check(t, "nothing is overdue yet", cov.LedgerOverdue, 0)

	late := CoverEvidence([]Chain{good, miss, young}, all, 3010+gate.LedgerGrace)
	check(t, "past the grace the young one is overdue", late.LedgerOverdue, 1)
	check(t, "and no longer pending", late.Pending, 0)
	check(t, "no scope excluded anything", cov.UnknownScope, 0)
	check(t, "nothing is ineligible", cov.Ineligible, 0)

	preview := CoverEvidence([]Chain{good, miss, young}, all, 0)
	check(t, "a preview counts scope standing only", preview.Eligible, 3)
}

func scopedRecord(ts int) Record {
	return Record{
		"id": "r", "class": "c", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": ts,
		"scope": map[string]any{"version": ScopeVersion, "all": true},
	}
}

func withClock(at int64, fn func()) {
	prev := now
	now = func() int64 { return at }
	defer func() { now = prev }()
	fn()
}
