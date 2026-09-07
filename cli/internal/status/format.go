// Shared phrasing for the measurement contract. Terminal, HTML and TUI all read one
// snapshot, so they name a range, a scope and a window's coverage with one wording.

package status

import (
	"fmt"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/tier"
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
func RequiredPhrase(required *int, unit, verdict string) string {
	if required == nil {
		if verdict == "collecting" || verdict == "inconclusive" {
			return "no clean window of any size could reach the target"
		}
		return ""
	}
	if *required == 0 {
		return ""
	}
	return fmt.Sprintf("needs %d more clean, eligible %s", *required, UnitName(unit, *required))
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
	// "no decision recorded" covers both a row the resolver closed as gone and one
	// still owed; Posture.Unresolved counts only the latter, so the two must not
	// share a word.
	if c.UnknownEvidence > 0 {
		extra = append(extra, fmt.Sprintf("%d with no decision recorded", c.UnknownEvidence))
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

// confoundWord names each kind in the reader's words. A hard confound says the two
// periods cannot be compared; a composition entry says only that the mix moved, and
// the scorer permitted the comparison, so it must not read as the harder claim.
var confoundWord = map[string]string{
	"confound":    "conditions changed",
	"composition": "the mix changed",
}

// ConfoundPhrases renders a confound list one line each, prefixed by its kind.
func ConfoundPhrases(kind string, cs []mine.Confound) []string {
	word := confoundWord[kind]
	if word == "" {
		word = kind
	}
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, fmt.Sprintf("%s (%s): %s", word, ConditionName(c.Category), c.Reason))
	}
	return out
}

// conditionName names what a compared condition IS, in the reader's words.
var conditionName = map[string]string{
	"instrument":   "how the evidence was produced",
	"host":         "the machine",
	"harness":      "the coding agent",
	"repo":         "the repository",
	"project":      "the project",
	"author_model": "the agent's own model",
	"path_kind":    "the kinds of file changed",
}

// ConditionName is that table's one reader. A category the table does not name
// prints verbatim. It is exported for the same reason UnitName is: the category
// reaches a renderer as stored data, so the vocabulary check cannot see it.
func ConditionName(category string) string {
	if name := conditionName[category]; name != "" {
		return name
	}
	return category
}

// ComparisonLimit is one sentence for a whole list of changed conditions: the
// claim once, then each condition named. A hard list says the periods cannot be
// compared; a soft list says only that the mix moved, which the scorer permitted,
// so the two never share a sentence. It is "" for an empty list.
func ComparisonLimit(kind string, cs []mine.Confound) string {
	if len(cs) == 0 {
		return ""
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, ConditionName(c.Category))
	}
	claim := "the mix of work changed, which qualifies this result"
	if kind == "confound" {
		claim = "these two periods ran under different conditions and cannot be compared"
	}
	return claim + " — " + strings.Join(names, "; ")
}

// ComparisonCauses lists each changed condition for the detail view: the
// condition named, then the reason. It repeats no claim, because ComparisonLimit
// above it made the claim once.
func ComparisonCauses(cs []mine.Confound) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, ConditionName(c.Category)+" — "+c.Reason)
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
		return "this machine keeps no guard audit log, so no block can be joined"
	}
	if a.Firings == 0 {
		return fmt.Sprintf("the %s guard blocked nothing since install", a.HookID)
	}
	return fmt.Sprintf("the %s guard blocked %d %s in %d agent %s",
		a.HookID, a.Firings, plural("action", a.Firings),
		a.Sessions, plural("session", a.Sessions))
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

// MasterPhrase names one registered master's runtime state in one clause. The
// empty string means the harness registers no master, which the caller renders
// as its own row or omits.
func MasterPhrase(m MasterRuntime) string {
	switch m.State {
	case MasterCurrent:
		return fmt.Sprintf("runtime %s, steer %s", m.Runtime, m.Version)
	case MasterStale:
		return fmt.Sprintf("runtime %s is older than this binary's %s — run: steer hooks rebuild --scope %s",
			m.Runtime, m.Expected, m.Scope)
	case MasterUnknown:
		return fmt.Sprintf("runtime has no version (unstamped) — run: steer hooks rebuild --scope %s", m.Scope)
	}
	return ""
}

// ResolvePhrase names what this process can say about the command a registration
// points at. It is a proxy for enforcement, never a proof of it: the hook runs
// under the harness's environment, whose PATH may differ from this one's. So a
// resolvable command says nothing worth printing, and only a missing one does.
func ResolvePhrase(a ScopeArming) string {
	if a.Resolvable == ResolveMissing {
		return "the command does not resolve here — the hook is skipped silently; " +
			"run: steer bin install"
	}
	return ""
}

// ObservedPhrase names when this channel last wrote a row of its own. An empty
// string means the channel writes no corpus, so nothing about it is observable.
// "never observed" is not proof of breakage: nobody may have attempted the action
// the channel fires on.
func ObservedPhrase(a ScopeArming) string {
	if a.ObservedCorpus == "" {
		return ""
	}
	if a.Observed == 0 {
		return "never observed"
	}
	return "last row " + time.Unix(int64(a.Observed), 0).Format("2006-01-02 15:04")
}

// MasterObservedPhrase names the last block a registered master recorded. The
// audit log holds blocks only, so a master that allowed every call has none.
func MasterObservedPhrase(m MasterRuntime) string {
	if m.State == MasterNotRegistered {
		return ""
	}
	if m.Observed == 0 {
		return "no block observed"
	}
	return "last block " + time.Unix(int64(m.Observed), 0).Format("2006-01-02 15:04")
}

// BackfillCommand clears the ledger rows that still owe an adjudication. Terminal
// and HTML both name it beside the count, so it is written once.
const BackfillCommand = "steer commit-gate backfill --apply"

// OutcomesWaitingPhrase names the reviews whose result is not known yet: the gate
// wrote that it reviewed, and nothing has yet read back what the coding agent did
// with the finding. Terminal, HTML and the dashboard all say it, so the wording
// and the plural live here rather than in three format strings.
func OutcomesWaitingPhrase(n int) string {
	if n == 1 {
		return "1 review has no outcome yet"
	}
	return fmt.Sprintf("%d reviews have no outcome yet", n)
}

// RetireCommand is the repair the removal prose describes: it takes a record out of
// the inventory. Terminal and HTML both print it under an unmeasurable or harmful
// verdict, so neither can name a different command from the other.
func RetireCommand(id string) string {
	return "steer fix retire " + id + " --reason ..."
}

// TierPhrase names what kind of intervention a record installed, in the reader's
// words rather than the flag's. A tier is a type and never a behavior, so it stands
// beside the intervention line rather than in place of it.
func TierPhrase(t, target string) string {
	if t == "" || t == "none" {
		return "no fix installed"
	}
	kind, _ := tier.Of(t)
	name := tier.Name(kind)
	if name == "" {
		name = t
	}
	if target == "" {
		return name
	}
	return name + " → " + target
}
