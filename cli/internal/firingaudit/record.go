package firingaudit

import (
	"fmt"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Case is one firing assembled for reading: the row, the action it blocked, and
// what the session did next.
type Case struct {
	Row          Row                     `json:"row"`
	Evidence     Evidence                `json:"evidence"`
	Continuation transcript.Continuation `json:"continuation"`
	// Prior are the judgments already made of this firing, oldest first. The
	// stream is append-only, so a re-read supersedes rather than replaces.
	Prior []adjudication.Receipt `json:"prior,omitempty"`
}

// Show assembles one firing for reading.
//
// The row's sidecar resolves against the store it was read from, so no caller
// can hand it the wrong one. An unresolvable bundle or continuation is reported
// in place rather than failing the call: judging the action needs the row, and
// the rest are separate facts about what could be read.
func Show(rows []Row, firingID string, receipts []adjudication.Receipt,
	budget transcript.ContinuationBudget) (Case, error) {
	row, ok := Find(rows, firingID)
	if !ok {
		return Case{}, fmt.Errorf("no audit row carries the identifier %q", firingID)
	}
	continuation := row.continuationOrUnavailable(budget)
	var prior []adjudication.Receipt
	for _, r := range receipts {
		if r.Subject == adjudication.SubjectGuard && r.FiringID == firingID {
			prior = append(prior, r)
		}
	}
	return Case{
		Row:          row,
		Evidence:     row.Resolve(row.StoreDir),
		Continuation: continuation,
		Prior:        prior,
	}, nil
}

// Judgment is what a reader concluded. Everything else on a receipt is resolved
// from the row, so this is the whole of what the reader supplies.
type Judgment struct {
	FiringID  string
	Judgment  string
	Rationale string
	// Evidence names what was read, beyond the anchors Record stamps itself.
	Evidence []string
	// ContinuationInspected reports that the reader read what the session did
	// next. Record refuses it when no join reached one.
	ContinuationInspected bool
	ContinuationOutcome   string
	// InspectedSteps is how many continuation steps the reader actually read.
	// Show can withhold steps to fit a byte allowance, so Record will not infer
	// this from the lookup: an unstated extent stays zero rather than claiming
	// everything the join reached.
	InspectedSteps int
	// Selection records why this firing was read, so a later pass advances
	// coverage instead of repeating it.
	Selection string
}

// Record writes one judgment to the adjudication stream in dir.
//
// The reader supplies the judgment and nothing else: Record finds the row,
// copies its implementation stamps, and resolves the continuation to stamp the
// join granularity, its anchors and its extent. It returns the receipt written.
func Record(rows []Row, dir string, j Judgment,
	budget transcript.ContinuationBudget) (adjudication.Receipt, error) {
	row, ok := Find(rows, j.FiringID)
	if !ok {
		return adjudication.Receipt{},
			fmt.Errorf("no audit row carries the identifier %q", j.FiringID)
	}
	// A transcript that will not open leaves the continuation unavailable. It
	// does not stop the judgment: the row carries the attempted action, which
	// is what settles whether the block was right.
	continuation := row.continuationOrUnavailable(budget)
	if j.ContinuationInspected && !continuation.Reached() {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports an inspected continuation and none was reached: %s",
			continuation.Reason)
	}
	if j.InspectedSteps > 0 && !j.ContinuationInspected {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports %d inspected continuation step(s) and no inspected continuation",
			j.InspectedSteps)
	}
	if j.InspectedSteps > len(continuation.Steps) {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports %d inspected continuation step(s) and the lookup reached %d",
			j.InspectedSteps, len(continuation.Steps))
	}
	anchors := continuation.Anchors
	receipt := adjudication.Receipt{
		Subject:   adjudication.SubjectGuard,
		FiringID:  row.FiringID,
		HookID:    row.HookName,
		Harness:   row.Harness,
		Judgment:  j.Judgment,
		Rationale: j.Rationale,
		Evidence:  j.Evidence,

		ContinuationInspected: j.ContinuationInspected,
		ContinuationOutcome:   j.ContinuationOutcome,
		Join:                  continuation.Granularity,
		JoinAnchors:           &anchors,
		JoinSteps:             len(continuation.Steps),
		InspectedSteps:        j.InspectedSteps,

		Selection: j.Selection,
		JudgedAt:  time.Now().UTC().Format(time.RFC3339),

		HookDigest:        row.HookDigest,
		RuleHash:          row.RuleHash,
		MasterBuildDigest: row.MasterBuildDigest,
	}
	if err := adjudication.Append(dir, receipt); err != nil {
		return adjudication.Receipt{}, err
	}
	return receipt, nil
}
