package firingaudit

import (
	"fmt"
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

// Counts is the whole selected queue by reason, taken before Limit cuts the
// listing. A pass that reads the listing alone cannot say what it left behind,
// because Limit removes rows silently; these counts are what a disclosure
// sentence is composed from.
type Counts struct {
	Unjudged           int `json:"unjudged"`
	ContinuationUnread int `json:"continuation_unread"`
	// Excluded is the selected rows an exclusion retires. They are not queued
	// and not unexamined work.
	Excluded int `json:"excluded"`
	// Queued is Unjudged + ContinuationUnread: everything the options selected.
	Queued int `json:"queued"`
	// Listed is how many of Queued survived Limit.
	Listed int `json:"listed"`
}

// Remaining states what a pass reading the listing has not taken on. It is the
// one wording for that sentence, so the CLI and a skill reading --json do not
// compose two different ones from the same counts.
func (c Counts) Remaining() string {
	left := c.Queued - c.Listed
	switch {
	case c.Queued == 0:
		return "Nothing is queued for reading."
	case left == 0:
		return fmt.Sprintf("All %d queued firing(s) are listed: %d unread, %d judged with an unread continuation.",
			c.Queued, c.Unjudged, c.ContinuationUnread)
	default:
		return fmt.Sprintf("%d of %d queued firing(s) listed; %d stay unexamined by this listing (%d unread, %d judged with an unread continuation in total).",
			c.Listed, c.Queued, left, c.Unjudged, c.ContinuationUnread)
	}
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
	// Excluded holds the rows an exclusion retires, keyed by row digest.
	Excluded map[string]adjudication.Exclusion
}

// Queue lists the firings a reading pass should take next, oldest first.
//
// Two things put a firing here: no receipt names it, or a receipt judged it
// without reading the continuation that is still on disk. The judgment's value
// is not one of them, so an unresolved receipt waits for new evidence or for
// the operator to ask for it by name.
func Queue(rows []Row, receipts []adjudication.Receipt, opts QueueOptions) ([]Item, Counts) {
	latest := adjudication.Latest(receipts)
	var items []Item
	excluded := 0
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
		if _, ok := opts.Excluded[row.RowDigest]; ok && row.RowDigest != "" {
			excluded++
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
	counts := Counts{Excluded: excluded}
	for _, item := range items {
		switch item.Reason {
		case Unjudged:
			counts.Unjudged++
		case ContinuationUnread:
			counts.ContinuationUnread++
		}
	}
	counts.Queued = len(items)
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	counts.Listed = len(items)
	return items, counts
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
		RowDigest:         r.RowDigest,
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
