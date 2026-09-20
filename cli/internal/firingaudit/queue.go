package firingaudit

import (
	"sort"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Why a firing is queued for reading.
const (
	// Unjudged means no receipt names this firing.
	Unjudged = "unjudged"
	// ContinuationUnread means a receipt judged the block and nobody read what
	// the task did next, while the transcript to read is still on this machine.
	ContinuationUnread = "continuation-unread"
)

// Item is one firing the queue hands over, with why it is there.
type Item struct {
	FiringID  string `json:"firing_id"`
	HookName  string `json:"hook_name"`
	Harness   string `json:"harness"`
	Timestamp string `json:"timestamp"`
	Rule      string `json:"rule,omitempty"`
	// Reason is Unjudged or ContinuationUnread.
	Reason string `json:"reason"`
	// Applies is the row's own implementation against the installed guard:
	// adjudication.Current, Historic or Unknown.
	Applies string `json:"applies"`
	// Reach is the join a lookup could achieve for this row.
	Reach string `json:"reach"`
}

// QueueOptions selects what the queue offers.
type QueueOptions struct {
	// HookName narrows to one guard. Empty takes every guard.
	HookName string
	// Installed returns the implementation installed for a row's guard. It is a
	// function because a row's store decides which master answers for it, and
	// resolving a store is the adapters' work. A nil resolver classifies every
	// row Unknown.
	Installed func(Row) adjudication.Identity
	// Other includes the rows an implementation other than the installed one
	// recorded. A reading pass cannot discharge them, so they are left out
	// unless the caller asks.
	Other bool
	// Limit caps the items returned, oldest first. Zero returns all of them.
	Limit int
}

// Queue lists the firings a reading pass should take next, oldest first.
//
// Two things put a firing here: no receipt names it, or a receipt judged it
// without reading the continuation that is still on disk. The judgment's value
// is not one of them, so an unresolved receipt waits for new evidence or for
// the operator to ask for it by name.
func Queue(rows []Row, receipts []adjudication.Receipt, opts QueueOptions) []Item {
	latest := adjudication.Latest(receipts)
	var items []Item
	for _, row := range rows {
		if opts.HookName != "" && row.HookName != opts.HookName {
			continue
		}
		if row.FiringID == "" {
			continue
		}
		var identity adjudication.Identity
		if opts.Installed != nil {
			identity = opts.Installed(row)
		}
		applies := adjudication.AppliesStamps(row.HookDigest, row.RuleHash, identity)
		if applies != adjudication.Current && !opts.Other {
			continue
		}
		reach := row.Reach()
		item := Item{
			FiringID:  row.FiringID,
			HookName:  row.HookName,
			Harness:   row.Harness,
			Timestamp: row.Timestamp,
			Rule:      row.Rule,
			Applies:   applies,
			Reach:     reach.Granularity,
		}
		receipt, judged := latest[row.FiringID]
		switch {
		case !judged:
			item.Reason = Unjudged
		case !receipt.ContinuationInspected && reach.Granularity != transcript.JoinUnavailable:
			item.Reason = ContinuationUnread
		default:
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Timestamp < items[j].Timestamp
	})
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items
}

// Firing reduces a row to the counting unit the coverage arithmetic reads, so
// both sides of a report count the same rows.
func (r Row) Firing() mine.Firing {
	return mine.Firing{
		HookID:            r.HookName,
		Harness:           r.Harness,
		SessionID:         r.SessionID,
		FiringID:          r.FiringID,
		HookDigest:        r.HookDigest,
		RuleHash:          r.RuleHash,
		MasterBuildDigest: r.MasterBuildDigest,
	}
}

// Firings reduces every row, for a caller handing them to adjudication.Cover.
func Firings(rows []Row) []mine.Firing {
	out := make([]mine.Firing, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Firing())
	}
	return out
}
