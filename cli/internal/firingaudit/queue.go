package firingaudit

import (
	"fmt"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
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

// Where a queued case stands in the reading order.
const (
	// StratumUnit is a case its unit still owes: the unit holds fewer than
	// SaturationReadings clean readings under its current identity, or a reading
	// that found the block wrong.
	StratumUnit = "unit"
	// StratumReserve is a case of a saturated unit. It is listed after every owed
	// case, so a pass reaches it only when nothing else is owed.
	StratumReserve = "reserve"
	// StratumOther is a case no reading of which describes the fix installed now,
	// listed only when the caller asks for --other.
	StratumOther = "other"
)

// SaturationReadings is how many clean readings under its current identity move
// a unit's remaining cases to the reserve. A clean reading read the continuation
// and settled the case, and found the block right.
const SaturationReadings = 3

// Item is one case the queue hands over, with why it is there: a guard firing,
// named by FiringID, or a check block, named by RunID.
type Item struct {
	Subject   string `json:"subject"`
	FiringID  string `json:"firing_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	HookName  string `json:"hook_name,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Harness   string `json:"harness"`
	Timestamp string `json:"timestamp"`
	Rule      string `json:"rule,omitempty"`
	// Reason is Unjudged or ContinuationUnread.
	Reason string `json:"reason"`
	// Stratum is StratumUnit, StratumReserve or StratumOther, and Unit the fix
	// whose turn in the order this case takes: a guard in its store, or one
	// failure kind of one repository's check.sh.
	Stratum string `json:"stratum"`
	Unit    string `json:"unit"`
	// Applies is a guard row's own implementation against the installed guard:
	// adjudication.Current, Historic or Unknown. A check block has no one
	// implementation; each of its Failures carries its own.
	Applies  string        `json:"applies,omitempty"`
	Failures []ItemFailure `json:"failures,omitempty"`
	// Reach is the join a lookup could achieve for this case.
	Reach string `json:"reach"`

	// covers is every owed unit a reading of this case advances, and moved the
	// units of its moved failures.
	covers []string
	moved  []string
}

// ItemFailure is one failure of a queued check block and what a reading of it
// would say about the detector installed now.
type ItemFailure struct {
	Kind     string `json:"kind"`
	Check    string `json:"check,omitempty"`
	Required bool   `json:"required"`
	Applies  string `json:"applies"`
	// Moved says the records show the detector changed before the check's next
	// pass, or the check was no longer declared at the session's next Stop. Such
	// a failure stays owed although its detector is not the installed one.
	Moved bool `json:"moved,omitempty"`
}

// ID is the identifier the item's subject is named by.
func (i Item) ID() string {
	if i.Subject == adjudication.SubjectCheck {
		return i.RunID
	}
	return i.FiringID
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
	// Other is the unread cases left for --other, because the records do not show
	// that a reading of them describes the fix installed now. Zero when --other
	// lists them.
	Other int `json:"other"`
	// Queued is Unjudged + ContinuationUnread: everything the options selected.
	Queued int `json:"queued"`
	// Reserve is how many of Queued belong to saturated units.
	Reserve int `json:"reserve"`
	// Listed is how many of Queued survived Limit.
	Listed int `json:"listed"`
}

// Remaining states what a pass reading the listing has not taken on. It is the
// one wording for that sentence, so the CLI and a skill reading --json do not
// compose two different ones from the same counts.
func (c Counts) Remaining() string {
	left := c.Queued - c.Listed
	var out string
	switch {
	case c.Queued == 0:
		out = "Nothing is queued for reading."
	case left == 0:
		out = fmt.Sprintf("All %d queued case(s) are listed: %d unread, %d judged with an unread continuation.",
			c.Queued, c.Unjudged, c.ContinuationUnread)
	default:
		out = fmt.Sprintf("%d of %d queued case(s) listed; %d stay unexamined by this listing (%d unread, %d judged with an unread continuation in total).",
			c.Listed, c.Queued, left, c.Unjudged, c.ContinuationUnread)
	}
	if c.Reserve > 0 {
		out += fmt.Sprintf(" %d queued case(s) are in the reserve: their fix already has %d clean readings under its current identity.",
			c.Reserve, SaturationReadings)
	}
	if c.Other > 0 {
		out += fmt.Sprintf(" %d more unread case(s) are listed only with --other: the records do not show that a reading of them describes the fix installed now.", c.Other)
	}
	return out
}

// QueueOptions selects what the queue offers.
type QueueOptions struct {
	// Subject narrows to adjudication.SubjectGuard or SubjectCheck. Empty takes both.
	Subject string
	// HookName narrows to one guard, and so to guard firings. Empty takes every guard.
	HookName string
	// Installed returns the implementation installed for a row's guard. It is a
	// function because a row's store decides which master answers for it, and
	// resolving a store is the adapters' work. A nil resolver classifies every
	// row Unknown.
	Installed func(Row) adjudication.Identity
	// Other includes the cases no reading of which describes the fix installed
	// now: guard rows another implementation recorded, and check blocks none of
	// whose declared-check failures is current or moved. A reading pass cannot
	// discharge them, so they are left out unless the caller asks.
	Other bool
	// Limit caps the items returned, in queue order. Zero returns all of them.
	Limit int
	// Excluded holds the rows an exclusion retires, keyed by row digest, and
	// ExcludedRuns the check blocks one retires, keyed by run.
	Excluded     map[string]adjudication.Exclusion
	ExcludedRuns map[string]adjudication.Exclusion
}

// Queue lists the cases a reading pass should take next, guard firings and check
// blocks in one order.
//
// Two things put a case here: no receipt names it, or a receipt judged it
// without reading the continuation that is still on disk. The judgment's value
// is not one of them, so an unresolved receipt waits for new evidence or for
// the operator to ask for it by name.
//
// Owed units go round-robin (see roundRobin), then the reserve the same way,
// then the --other cases oldest first.
func Queue(rows []Row, cases []CheckCase, receipts []adjudication.Receipt, opts QueueOptions) ([]Item, Counts) {
	tallies := tallyReadings(rows, cases, receipts, opts)
	var items []Item
	var counts Counts
	if opts.Subject != adjudication.SubjectCheck {
		guards, excluded, other := guardItems(rows, receipts, opts, tallies)
		items = append(items, guards...)
		counts.Excluded += excluded
		counts.Other += other
	}
	if opts.Subject != adjudication.SubjectGuard && opts.HookName == "" {
		blocks, excluded, other := checkItems(cases, receipts, opts, tallies)
		items = append(items, blocks...)
		counts.Excluded += excluded
		counts.Other += other
	}
	sort.SliceStable(items, func(i, j int) bool {
		return Earlier(items[i].Timestamp, items[j].Timestamp)
	})
	for _, item := range items {
		switch item.Reason {
		case Unjudged:
			counts.Unjudged++
		case ContinuationUnread:
			counts.ContinuationUnread++
		}
		if item.Stratum == StratumReserve {
			counts.Reserve++
		}
	}
	items = arrange(items, tallies)
	counts.Queued = len(items)
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	counts.Listed = len(items)
	return items, counts
}

// Earlier orders two timestamps by the instant they name, falling back to their
// text when either does not parse, so a guard row's offset and a check run's UTC
// time sort together.
func Earlier(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA != nil || errB != nil {
		return a < b
	}
	return ta.Before(tb)
}

// revisit returns why a judged or unjudged case is queued, or "" when it is not.
func revisit(receipt adjudication.Receipt, judged bool, reach string) string {
	switch {
	case !judged:
		return Unjudged
	case !receipt.ContinuationInspected && reach != transcript.JoinUnavailable:
		return ContinuationUnread
	}
	return ""
}

// readings is one unit's account of the readings made under its current
// identity: how many were clean, and whether any found the block wrong.
// Unmanaged is check.sh's or the wiring's own unit, which has no identity to
// tell its readings' implementation from the one running now.
type readings struct {
	clean     int
	wrong     bool
	unmanaged bool
}

func (r readings) saturated() bool {
	return !r.unmanaged && !r.wrong && r.clean >= SaturationReadings
}

// guardUnit names a guard's unit. Two stores can hold a guard of the same name,
// and their rows are not interchangeable.
func guardUnit(row Row) string {
	if row.StoreDir == "" {
		return "guard:" + row.HookName
	}
	return "guard:" + row.HookName + "@" + row.StoreDir
}

// checkUnit names one failure kind of one repository's check.sh.
func checkUnit(repo, kind, check string) string {
	return FailureName(kind, check) + "@" + repo
}

// moved reports that the records show a declared-check failure's detector
// changed before its next pass, or that the session's next Stop no longer
// declared it. It reads the follow-up of the execution that reported f.
func (c CheckCase) moved(f CheckFailure) bool {
	if f.Kind != adjudication.FailureCheck {
		return false
	}
	for _, follow := range c.Followup.Checks {
		if follow.Blocked.CheckID != f.Check {
			continue
		}
		if e := f.Execution; e != nil &&
			(follow.Blocked.Invocation != e.Invocation || !follow.Blocked.Started.Equal(e.Started)) {
			continue
		}
		return follow.Detector == checks.FollowChanged || follow.Undeclared
	}
	return false
}

// units returns the failures whose units a reading of this block advances:
// every failure but check.sh's own beside a failing declared check, because
// check.sh exits non-zero for that check. owed marks which of them leave the
// block owed.
func (c CheckCase) units() (failures []CheckFailure, owed []bool) {
	declared := false
	for _, f := range c.Failures {
		declared = declared || f.Kind == adjudication.FailureCheck
	}
	for _, f := range c.Failures {
		if declared && f.Kind == adjudication.FailureOracle {
			continue
		}
		failures = append(failures, f)
		owed = append(owed, f.Kind != adjudication.FailureCheck ||
			f.Applies == adjudication.Current || c.moved(f))
	}
	return failures, owed
}

// tallyReadings accounts every unit's latest readings under its current
// identity, leaving out excluded cases. A guard reading counts when the
// receipt's stamps are the installed guard's; a check reading counts per
// failure, when the block's detector for it is the installed one or the failure
// has no detector of its own.
func tallyReadings(rows []Row, cases []CheckCase, receipts []adjudication.Receipt,
	opts QueueOptions) map[string]readings {
	out := map[string]readings{}
	byFiring := map[string]Row{}
	for _, row := range rows {
		if _, excluded := opts.Excluded[row.RowDigest]; excluded && row.RowDigest != "" {
			continue
		}
		if row.FiringID != "" {
			byFiring[row.FiringID] = row
		}
	}
	for id, receipt := range adjudication.Latest(receipts, adjudication.SubjectGuard) {
		row, ok := byFiring[id]
		if !ok || opts.Installed == nil {
			continue
		}
		if adjudication.AppliesStamps(receipt.HookDigest, receipt.RuleHash, opts.Installed(row)) != adjudication.Current {
			continue
		}
		unit := guardUnit(row)
		r := out[unit]
		switch {
		case receipt.Judgment == adjudication.Incorrect:
			r.wrong = true
		case receipt.ContinuationInspected && receipt.InspectedSteps > 0 &&
			receipt.Judgment == adjudication.Prevented:
			r.clean++
		}
		out[unit] = r
	}
	byRun := map[string]CheckCase{}
	for _, c := range cases {
		byRun[c.Run.RunID] = c
	}
	for id, receipt := range adjudication.Latest(receipts, adjudication.SubjectCheck) {
		c, ok := byRun[id]
		if _, excluded := opts.ExcludedRuns[id]; !ok || excluded {
			continue
		}
		failures, _ := c.units()
		for _, f := range failures {
			if f.Kind == adjudication.FailureCheck && f.Applies != adjudication.Current {
				continue
			}
			read, ok := receiptFailure(receipt, f)
			if !ok {
				continue
			}
			unit := checkUnit(c.Run.Repo, f.Kind, f.Check)
			r := out[unit]
			r.unmanaged = f.Kind != adjudication.FailureCheck
			switch {
			case read.Validity == adjudication.Invalid || read.Continuation == adjudication.RoutedAround:
				r.wrong = true
			case receipt.ContinuationInspected && receipt.InspectedSteps > 0 && read.Validity == adjudication.Valid &&
				(read.Continuation == adjudication.Fixed || read.Continuation == adjudication.Declined):
				r.clean++
			}
			out[unit] = r
		}
	}
	return out
}

// receiptFailure finds a receipt's reading of one failure.
func receiptFailure(receipt adjudication.Receipt, f CheckFailure) (adjudication.Failure, bool) {
	for _, read := range receipt.Failures {
		if read.Kind == f.Kind && read.Check == f.Check {
			return read, true
		}
	}
	return adjudication.Failure{}, false
}

// place sets an owed item's stratum: the reserve when every unit it covers is
// saturated. roundRobin picks the unit whose turn it takes.
func place(item *Item, covers []string, tallies map[string]readings) {
	item.covers = covers
	item.Unit = covers[0]
	item.Stratum = StratumReserve
	for _, unit := range covers {
		if !tallies[unit].saturated() {
			item.Stratum = StratumUnit
		}
	}
}

// arrange orders time-sorted items: owed units round-robin, then the reserve
// round-robin, then the --other cases as they came.
func arrange(items []Item, tallies map[string]readings) []Item {
	var owed, reserve, other []Item
	for _, item := range items {
		switch item.Stratum {
		case StratumUnit:
			owed = append(owed, item)
		case StratumReserve:
			reserve = append(reserve, item)
		default:
			other = append(other, item)
		}
	}
	out := append(roundRobin(owed, tallies, true), roundRobin(reserve, tallies, false)...)
	return append(out, other...)
}

// roundRobin gives each turn to the unit with the fewest readings and lists its
// oldest unlisted case, counting that case as one more reading of every unit it
// covers. A tie goes to the unit whose oldest case is oldest. In the owed
// stratum a case with a moved failure takes only its moved units' turns, and a
// saturated unit takes a turn only for a case no other unit covers.
func roundRobin(items []Item, tallies map[string]readings, owed bool) []Item {
	var order []string
	queues := map[string][]int{}
	count := map[string]int{}
	for i, item := range items {
		var turns []string
		for _, unit := range item.covers {
			if !owed || !tallies[unit].saturated() {
				turns = append(turns, unit)
			}
		}
		if len(turns) == 0 {
			turns = item.covers
		}
		if owed && len(item.moved) > 0 {
			turns = item.moved
		}
		for _, unit := range turns {
			if _, ok := queues[unit]; !ok {
				order = append(order, unit)
				count[unit] = tallies[unit].clean
			}
			queues[unit] = append(queues[unit], i)
		}
	}
	listed := make([]bool, len(items))
	out := make([]Item, 0, len(items))
	for len(out) < len(items) {
		next := ""
		for _, unit := range order {
			q := queues[unit]
			for len(q) > 0 && listed[q[0]] {
				q = q[1:]
			}
			queues[unit] = q
			if len(q) == 0 {
				continue
			}
			if next == "" || count[unit] < count[next] ||
				(count[unit] == count[next] && Earlier(items[q[0]].Timestamp, items[queues[next][0]].Timestamp)) {
				next = unit
			}
		}
		if next == "" {
			break
		}
		i := queues[next][0]
		listed[i] = true
		item := items[i]
		item.Unit = next
		for _, unit := range item.covers {
			count[unit]++
		}
		out = append(out, item)
	}
	return out
}

// checkItems lists the check blocks a reading pass should take, how many an
// exclusion retires, and how many only --other lists.
func checkItems(cases []CheckCase, receipts []adjudication.Receipt, opts QueueOptions,
	tallies map[string]readings) ([]Item, int, int) {
	latest := adjudication.Latest(receipts, adjudication.SubjectCheck)
	var items []Item
	excluded, other := 0, 0
	for _, c := range cases {
		if _, ok := opts.ExcludedRuns[c.Run.RunID]; ok {
			excluded++
			continue
		}
		receipt, judged := latest[c.Run.RunID]
		reach := c.Reach()
		reason := revisit(receipt, judged, reach.Granularity)
		if reason == "" {
			continue
		}
		failures, owed := c.units()
		var covers []string
		for i, f := range failures {
			if owed[i] {
				covers = append(covers, checkUnit(c.Run.Repo, f.Kind, f.Check))
			}
		}
		if len(covers) == 0 && !opts.Other {
			other++
			continue
		}
		item := Item{
			Subject:   adjudication.SubjectCheck,
			RunID:     c.Run.RunID,
			Repo:      c.Run.Repo,
			Harness:   c.Run.Harness,
			Timestamp: c.Run.Started.UTC().Format(time.RFC3339Nano),
			Reason:    reason,
			Reach:     reach.Granularity,
		}
		for _, f := range c.Failures {
			moved := c.moved(f)
			item.Failures = append(item.Failures, ItemFailure{Kind: f.Kind, Check: f.Check,
				Required: f.Required, Applies: f.Applies, Moved: moved})
			if moved {
				item.moved = append(item.moved, checkUnit(c.Run.Repo, f.Kind, f.Check))
			}
		}
		if len(covers) == 0 {
			item.Stratum = StratumOther
			if len(failures) > 0 {
				item.Unit = checkUnit(c.Run.Repo, failures[0].Kind, failures[0].Check)
			}
		} else {
			place(&item, covers, tallies)
			if len(item.moved) > 0 {
				item.Stratum = StratumUnit
			}
		}
		items = append(items, item)
	}
	return items, excluded, other
}

// guardItems lists the guard firings a reading pass should take, how many
// selected rows an exclusion retires, and how many only --other lists.
func guardItems(rows []Row, receipts []adjudication.Receipt, opts QueueOptions,
	tallies map[string]readings) ([]Item, int, int) {
	latest := adjudication.Latest(receipts, adjudication.SubjectGuard)
	var items []Item
	excluded, other := 0, 0
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
		_, isExcluded := opts.Excluded[row.RowDigest]
		isExcluded = isExcluded && row.RowDigest != ""
		reach := row.Reach()
		receipt, judged := latest[row.FiringID]
		reason := revisit(receipt, judged, reach.Granularity)
		if applies != adjudication.Current && !opts.Other {
			if !isExcluded && reason != "" {
				other++
			}
			continue
		}
		if isExcluded {
			excluded++
			continue
		}
		if reason == "" {
			continue
		}
		item := Item{
			Subject:   adjudication.SubjectGuard,
			FiringID:  row.FiringID,
			HookName:  row.HookName,
			Harness:   row.Harness,
			Timestamp: row.Timestamp,
			Rule:      row.Rule,
			Reason:    reason,
			Unit:      guardUnit(row),
			Applies:   applies,
			Reach:     reach.Granularity,
		}
		if applies == adjudication.Current {
			place(&item, []string{item.Unit}, tallies)
		} else {
			item.Stratum = StratumOther
		}
		items = append(items, item)
	}
	return items, excluded, other
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
