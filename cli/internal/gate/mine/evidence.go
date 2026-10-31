package mine

import "github.com/steer-sh/steer-cli/internal/gate"

// Evidence is how complete a chain's adjudication is. A chain that owes a ledger has
// unknown findings, so counting it as a clean miss would pull the incidence rate down
// by exactly the rows the resolver has not read yet.
type Evidence int

const (
	// EvidenceComplete: every row that owes a ledger has one.
	EvidenceComplete Evidence = iota
	// EvidencePending: a row still owes a ledger and is inside the resolver's grace
	// window, so the ledger may yet arrive.
	EvidencePending
	// EvidenceMissed: the resolver searched for a row's ledger and closed it as a miss.
	// Nothing routine will change it, so it is disclosed rather than repaired.
	EvidenceMissed
	// EvidenceOverdue: the grace window shut on a row the resolver has not closed.
	EvidenceOverdue
)

// expectsLedger reports a row whose findings were handed to the agent and therefore owe
// an adjudication. It mirrors the resolver's own predicate in gate/resolve.
func expectsLedger(row gate.Row) bool {
	switch str(row["phase"]) {
	case "retry":
		return true
	case "cap":
		// A cap row reaches the agent with review text whenever the panel returned any
		// review at all. The strict count is one reading of that text, not the test of
		// whether it was handed over: a reviewer that wrote its findings as prose scores
		// zero slots and still costs the agent an adjudication. reviews_off_format
		// counts exactly those reviews, and it is absent on a row whose panel returned
		// nothing, so the clean cap still owes no ledger.
		return numInt(row["finding_slots"]) > 0 || numInt(row["reviews_off_format"]) > 0
	}
	return false
}

// rowAnchorTS is when the findings this row answers were handed over: a cap row is that
// moment, a retry row answers the deny before it.
func rowAnchorTS(rows []gate.Row, i int) (float64, bool) {
	if str(rows[i]["phase"]) == "cap" {
		return numFloat(rows[i]["ts"]), true
	}
	if v := numFloat(rows[i]["deny_ts"]); v != 0 {
		return v, true
	}
	for j := i - 1; j >= 0; j-- {
		if str(rows[j]["phase"]) == "deny" {
			return numFloat(rows[j]["ts"]), true
		}
	}
	return 0, false
}

// ChainEvidence grades one chain, worst row wins: unresolved outranks a closed miss, so a
// miss never hides work the resolver still owes. now is the clock the grace window is
// measured against.
func ChainEvidence(ch Chain, now float64) Evidence {
	worst := EvidenceComplete
	for i, row := range ch.Rows {
		if !expectsLedger(row) {
			continue
		}
		// The adjudication itself settles the row: a concerns key means the ledger was
		// read into it. ledger_found is the resolver's stamp for the same fact, and a
		// row written before the resolver existed carries only the concerns.
		if _, adjudicated := row["concerns"]; adjudicated || truthy(row["ledger_found"]) {
			continue
		}
		state := EvidenceMissed
		if !truthy(row["backfilled"]) {
			// The resolver has not closed this row. Only the grace window separates young
			// from overdue.
			state = EvidenceOverdue
			if _, stamped := row["ledger_found"]; !stamped {
				if anchor, ok := rowAnchorTS(ch.Rows, i); ok && now < anchor+gate.LedgerGrace {
					state = EvidencePending
				}
			}
		}
		if state > worst {
			worst = state
		}
	}
	return worst
}
