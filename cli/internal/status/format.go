// Shared phrasing for the measurement contract. Terminal, HTML and TUI all read one
// snapshot, so they name a range, a scope and a window's coverage with one wording.

package status

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// EffectPhrase renders the relative-risk range. An unbounded upper bound prints as
// ∞, and an absent estimate as —; an unmeasured record prints "".
func EffectPhrase(e cadence.Effect) string {
	if e.Method == "" {
		return ""
	}
	est := "—"
	if e.Estimate != nil {
		est = fmt.Sprintf("%.2f", *e.Estimate)
	}
	upper := "∞"
	if e.Upper != nil {
		upper = fmt.Sprintf("%.2f", *e.Upper)
	}
	return fmt.Sprintf("RR %s [%.2f, %s] @%.0f%%", est, e.Lower, upper, e.Confidence*100)
}

// RequiredPhrase names the clean eligible after-window that could still place the
// range under the effect target. It is "" when the record needs no more exposure and
// names the unreachable case when no window of any size could decide it.
func RequiredPhrase(required *int, verdict string) string {
	if required == nil {
		if verdict == "collecting" || verdict == "inconclusive" {
			return "no clean window of any size could reach the target"
		}
		return ""
	}
	if *required == 0 {
		return ""
	}
	return fmt.Sprintf("needs %d clean eligible chain(s)", *required)
}

// ScopePhrase names a record's frozen eligibility rule in one clause.
func ScopePhrase(sc *cadence.Scope) string {
	if sc == nil {
		return "none (legacy)"
	}
	if sc.All {
		return "all"
	}
	parts := []string{}
	if len(sc.Harness) > 0 {
		parts = append(parts, "harness="+strings.Join(sc.Harness, "|"))
	}
	if sc.Repo != "" {
		parts = append(parts, "repo=/"+sc.Repo+"/")
	}
	if sc.Path != "" {
		parts = append(parts, "path=/"+sc.Path+"/")
	}
	return strings.Join(parts, " AND ")
}

// CoveragePhrase reports one window's chain accounting under the scope.
func CoveragePhrase(c cadence.Coverage) string {
	s := fmt.Sprintf("%d eligible / %d", c.Eligible, c.Total)
	extra := []string{}
	if c.Ineligible > 0 {
		extra = append(extra, fmt.Sprintf("%d out of scope", c.Ineligible))
	}
	if c.UnknownScope > 0 {
		extra = append(extra, fmt.Sprintf("%d unknown scope", c.UnknownScope))
	}
	// "no ledger" covers both a row the resolver closed as gone and one still owed;
	// Posture.Unresolved counts only the latter, so the two must not share a word.
	if c.UnknownEvidence > 0 {
		extra = append(extra, fmt.Sprintf("%d without a ledger", c.UnknownEvidence))
	}
	if c.Pending > 0 {
		extra = append(extra, fmt.Sprintf("%d pending", c.Pending))
	}
	// A session that straddled the install ran under both regimes, so it is evidence
	// about neither window. It is not an unknown: the scope decided it fine.
	if c.PartialExposure > 0 {
		extra = append(extra, fmt.Sprintf("%d partly exposed", c.PartialExposure))
	}
	if len(extra) > 0 {
		s += " (" + strings.Join(extra, ", ") + ")"
	}
	return s
}

// ConfoundPhrases renders a confound list one line each, prefixed by its kind.
func ConfoundPhrases(kind string, cs []mine.Confound) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, fmt.Sprintf("%s (%s): %s", kind, c.Category, c.Reason))
	}
	return out
}

// AttributionPhrase reports the guard's audit join beside the range. An empty string
// means the join says nothing here: the tier writes no audit rows, or the record names
// no hook to join on.
func AttributionPhrase(a cadence.Attribution) string {
	if !a.Applicable {
		return ""
	}
	if !a.LogPresent {
		return "attribution: no guard audit stream on this machine"
	}
	if a.Firings == 0 {
		return fmt.Sprintf("attribution: %s never fired in the after-window", a.HookID)
	}
	return fmt.Sprintf("attribution: %s fired %d time(s) in %d session(s)",
		a.HookID, a.Firings, a.Sessions)
}

// TrustPhrase names a harness's posture on a registration. Registration alone does not
// prove enforcement on a harness that reviews hook trust itself.
func TrustPhrase(trust string) string {
	switch trust {
	case TrustRecordPresent:
		return "trust record present, unverified"
	case TrustMissing:
		return "no trust record"
	case TrustUnknown:
		return "trust unknown"
	}
	return ""
}
