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
	if r.HookDigest == "" || r.RuleHash == "" ||
		installed.HookDigest == "" || installed.RuleHash == "" {
		return Unknown
	}
	if r.HookDigest == installed.HookDigest && r.RuleHash == installed.RuleHash {
		return Current
	}
	return Historic
}

// Coverage accounts for one guard's audit stream against the judgments on hand.
// Rows == Adjudicable + Legacy, and Adjudicable == Judged + Unjudged.
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
	// Judged is the adjudicable rows a receipt names.
	Judged int `json:"judged"`
	// Unjudged is the adjudicable rows no receipt names. It is a coverage limit on
	// the existing reading, never a replacement for it.
	Unjudged int `json:"unjudged"`
	// Superseded is the judged rows whose judgment no longer describes the
	// installed guard. It is the signal that another pass is due, and it is
	// independent of how many rows one pass reads.
	Superseded int `json:"superseded"`
	// IdentityUnknown is the judged rows whose applicability neither side
	// establishes.
	IdentityUnknown int `json:"identity_unknown"`
}

// Cover counts one guard's rows against the receipts on hand. hookID selects the
// rows; an empty hookID counts every row given.
func Cover(hookID string, firings []mine.Firing, receipts []Receipt, installed Identity) Coverage {
	latest := Latest(receipts)
	var c Coverage
	for _, f := range firings {
		if hookID != "" && f.HookID != hookID {
			continue
		}
		c.Rows++
		if f.FiringID == "" {
			c.Legacy++
			continue
		}
		c.Adjudicable++
		receipt, judged := latest[f.FiringID]
		if !judged {
			c.Unjudged++
			continue
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

// Refresh reports whether another audit pass is due, and why. A changed
// implementation is due at any unjudged count; an unjudged count comes due only at
// the threshold the caller sets, and never on its own when that threshold is zero.
func Refresh(c Coverage, unjudgedThreshold int) (due bool, reason string) {
	if c.Superseded > 0 {
		return true, "the guard changed since it was judged"
	}
	if unjudgedThreshold > 0 && c.Unjudged >= unjudgedThreshold {
		return true, "unjudged firings reached the refresh threshold"
	}
	return false, ""
}
