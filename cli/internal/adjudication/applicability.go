package adjudication

import "github.com/steer-sh/steer-cli/internal/gate/mine"

// What a past judgment says about the guard installed now. A judgment of a recorded
// block stays true whatever happens to the guard afterwards; these states report
// only whether it also describes what is installed today.
const (
	// Current means the row's implementation matches the installed guard, so the
	// judgment describes the guard now running.
	Current = "current"
	// Historic means the guard changed after the judgment. The judgment stands as
	// a reading of that block; it validates no installed guard.
	Historic = "historic"
	// Unknown means one side carries no implementation identity, so neither a match
	// nor a change is established. Rows written before the master stamped its
	// identity read this, and that set does not grow.
	Unknown = "unknown"
)

// Identity is the installed guard's implementation, read from the registered
// master's own stamps by the caller. This package takes it as plain values, because
// resolving a store is the adapters' work and not the scorer's.
type Identity struct {
	HookDigest string
	RuleHash   string
}

// Applies reports what a receipt says about the identity installed now.
//
// Both stamps must be present on both sides. A digest that matches while the rule
// differs is a changed guard, so any disagreement is Historic.
func Applies(r Receipt, installed Identity) string {
	return AppliesStamps(r.HookDigest, r.RuleHash, installed)
}

// AppliesStamps is Applies over a pair of stamps, for a caller holding a row
// rather than a receipt. An unjudged row classifies by the same rule as a
// judged one, so both sides go through here.
func AppliesStamps(hookDigest, ruleHash string, installed Identity) string {
	if hookDigest == "" || ruleHash == "" ||
		installed.HookDigest == "" || installed.RuleHash == "" {
		return Unknown
	}
	if hookDigest == installed.HookDigest && ruleHash == installed.RuleHash {
		return Current
	}
	return Historic
}

// Coverage accounts for one guard's audit stream against the judgments on hand.
// Rows == Adjudicable + Legacy + Excluded, and Adjudicable == Judged + Unjudged. The four
// counts below it partition Adjudicable a second way, crossing whether a row is
// judged with whether the installed implementation recorded it.
type Coverage struct {
	// Rows is every recorded block for this guard.
	Rows int `json:"rows"`
	// Adjudicable is the rows carrying their own identifier. Only these can be
	// judged, because no other field of a row identifies it.
	Adjudicable int `json:"adjudicable"`
	// Legacy is the rows written before the master stamped an identifier. They are
	// not unread work: nothing can attach a judgment to them, and no later firing
	// joins this set.
	Legacy int `json:"legacy"`
	// Excluded is the rows an exclusion retires as recording no real work. They
	// stay in the stream and are counted nowhere else.
	Excluded int `json:"excluded"`
	// Judged is the adjudicable rows a receipt names.
	Judged int `json:"judged"`
	// Unjudged is the adjudicable rows no receipt names. It is a coverage limit on
	// the existing reading, never a replacement for it.
	Unjudged int `json:"unjudged"`
	// Superseded is the judged rows whose judgment no longer describes the
	// installed guard. It is a limit on what the existing reading covers, not work
	// waiting to be done: the rows it counts can never leave it.
	Superseded int `json:"superseded"`
	// IdentityUnknown is the judged rows whose applicability neither side
	// establishes.
	IdentityUnknown int `json:"identity_unknown"`

	// CurrentJudged and CurrentUnjudged are the adjudicable rows whose own stamps
	// match the installed implementation. CurrentUnjudged is the only count a
	// reading can move.
	CurrentJudged   int `json:"current_judged"`
	CurrentUnjudged int `json:"current_unjudged"`
	// OtherJudged and OtherUnjudged are the adjudicable rows some other
	// implementation recorded, or that carry no identity. With the two above they
	// account for every adjudicable row, as Judged and Unjudged do.
	OtherJudged   int `json:"other_judged"`
	OtherUnjudged int `json:"other_unjudged"`
}

// Cover counts one guard's rows against the receipts on hand. hookID selects the
// rows; an empty hookID counts every row given. excluded is keyed by row digest,
// as Excluded returns it.
func Cover(hookID string, firings []mine.Firing, receipts []Receipt, installed Identity,
	excluded map[string]Exclusion) Coverage {
	latest := Latest(receipts, SubjectGuard)
	var c Coverage
	for _, f := range firings {
		if hookID != "" && f.HookID != hookID {
			continue
		}
		c.Rows++
		if _, ok := excluded[f.RowDigest]; ok && f.RowDigest != "" {
			c.Excluded++
			continue
		}
		if f.FiringID == "" {
			c.Legacy++
			continue
		}
		c.Adjudicable++
		current := firingApplies(f, installed) == Current
		receipt, judged := latest[f.FiringID]
		if !judged {
			c.Unjudged++
			if current {
				c.CurrentUnjudged++
			} else {
				c.OtherUnjudged++
			}
			continue
		}
		if current {
			c.CurrentJudged++
		} else {
			c.OtherJudged++
		}
		c.Judged++
		switch Applies(receipt, installed) {
		case Historic:
			c.Superseded++
		case Unknown:
			c.IdentityUnknown++
		}
	}
	return c
}

// firingApplies reports what one row's recorded implementation is against the one
// installed now. It reads the row's own stamps rather than a receipt's, so an
// unjudged row classifies by the same rule as a judged one.
func firingApplies(f mine.Firing, installed Identity) string {
	return AppliesStamps(f.HookDigest, f.RuleHash, installed)
}

// Refresh reports whether another audit pass is due, and why. Both triggers read
// only the rows the installed implementation recorded: the first unread one is due
// at once, and the backlog after it at the threshold the caller sets. A zero
// threshold never comes due on count alone, and Superseded is not a trigger.
func Refresh(c Coverage, unjudgedThreshold int) (due bool, reason string) {
	if c.CurrentUnjudged > 0 && c.CurrentJudged == 0 {
		return true, "the installed guard has fired and none of it is read yet"
	}
	if unjudgedThreshold > 0 && c.CurrentUnjudged >= unjudgedThreshold {
		return true, "unread firings of the installed guard reached the refresh threshold"
	}
	return false, ""
}

// What a check failure's reading says about the detector installed now, beside
// Current, Historic and Unknown.
const (
	// Unmanaged means the failure has no detector of its own: check.sh itself
	// or its wiring failed, and Steer records no identity for either.
	Unmanaged = "unmanaged"
	// Unavailable means the installed detector could not be read: the
	// repository is not on this machine, or its manifest or detector files do
	// not read.
	Unavailable = "unavailable"
)

// DetectorApplies reports what a check failure recorded under recorded says
// about the repository's detectors now. installed maps each check the
// repository declares now to its detector digest, "" for one whose detector did
// not read; a nil map means the repository's checks could not be read. A check
// no longer declared is Historic: the reading validates no installed detector.
func DetectorApplies(kind, check, recorded string, installed map[string]string) string {
	if kind != FailureCheck {
		return Unmanaged
	}
	if installed == nil {
		return Unavailable
	}
	digest, declared := installed[check]
	switch {
	case !declared:
		return Historic
	case digest == "":
		return Unavailable
	case recorded == "":
		return Unknown
	case recorded == digest:
		return Current
	}
	return Historic
}
