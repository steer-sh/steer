// Shared phrasing for the measurement contract. Terminal and TUI read one snapshot,
// so they name a scope and a window's coverage with one wording.

package status

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/tier"
)

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

// RulesRepairCommand rewrites every installed harness's rules section in a scope.
func RulesRepairCommand(scope string) string {
	return "steer hooks rebuild --scope " + scope
}

// RulesPhrase names one harness's rules section in one clause: whether it carries
// every rule Steer holds for the scope. The repair is the caller's to name, since
// the terminal names a command and the dashboard names an action.
func RulesPhrase(r RulesPresence) string {
	switch r.State {
	case RulesCurrent:
		return fmt.Sprintf("all %d rule(s) present in %s", r.Expected, r.Path)
	case RulesUnknown:
		if r.Path == "" {
			return "the instruction file could not be found"
		}
		return "could not read " + r.Path
	}
	var parts []string
	if len(r.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("%s is missing %d of %d rule(s) (%s)",
			r.Path, len(r.Missing), r.Expected, strings.Join(r.Missing, ", ")))
	}
	if len(r.Extra) > 0 {
		lead := r.Path + " carries"
		if len(parts) > 0 {
			lead = "and carries"
		}
		parts = append(parts, fmt.Sprintf("%s %d line(s) Steer does not hold, which the next sync removes",
			lead, len(r.Extra)))
	}
	return strings.Join(parts, " ")
}

// RulesDrift summarizes the drifted sections for a one-line notice: a key that
// changes whenever the drift does, one label per drifted section, and each scope
// to rebuild, once. An empty key means no section drifted. An unreadable section
// is left out, because a rebuild does not repair a file it cannot read.
func RulesDrift(rows []RulesPresence) (key string, labels, scopes []string) {
	var parts []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r.State != RulesDrifted {
			continue
		}
		// A project path is relative to the repository, so the key resolves it:
		// the same drift in two repositories is two conditions.
		path := r.Path
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		parts = append(parts, fmt.Sprintf("%s/%s:%s:%s:%d",
			r.Harness, r.Scope, path, strings.Join(r.Missing, ","), len(r.Extra)))
		label := fmt.Sprintf("%s, %s scope", HarnessLabel(r.Harness), r.Scope)
		if len(r.Missing) > 0 {
			label += fmt.Sprintf(", is missing %d of %d", len(r.Missing), r.Expected)
		}
		if len(r.Extra) > 0 {
			label += fmt.Sprintf(", has %d line(s) Steer does not hold", len(r.Extra))
		}
		labels = append(labels, label)
		if !seen[r.Scope] {
			seen[r.Scope] = true
			scopes = append(scopes, r.Scope)
		}
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels, scopes
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

// RunnerUnapproved reports a registered runner the harness is known not to have
// approved, so it runs no guard there while the rule's text still reaches the agent.
func (m MasterRuntime) RunnerUnapproved() bool {
	return m.State != MasterNotRegistered && m.Trust == TrustMissing
}

// RunnerTrustPhrase names a registered runner's approval, "" where the harness
// reviews no hook trust.
func RunnerTrustPhrase(m MasterRuntime) string {
	if m.RunnerUnapproved() {
		return "no approval record, so the harness runs no guard — approve the definitions in /hooks"
	}
	return TrustPhrase(m.Trust)
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

// CheckAdoptionPhrase names whether this machine authorized the repository's
// oracle to run at the end of a turn. Adoption is machine-local: a clone of a
// repository that declares checks carries none of it.
func CheckAdoptionPhrase(c CheckTier) string {
	switch {
	case c.Repo == "":
		return "not inside a repository"
	case c.Adopted && c.AdoptedAt > 0:
		return "authorized here since " + time.Unix(int64(c.AdoptedAt), 0).Format("2006-01-02 15:04")
	case c.Adopted:
		return "authorized here"
	case c.Manifest:
		return "declares checks, not authorized to run here — run: steer check adopt"
	default:
		return "declares no checks"
	}
}

// CheckRunPhrase names the last run of the repository's oracle that did any
// work. A run turned away while another held the repository executed nothing, so
// it is reported beside that result and never in place of it.
func CheckRunPhrase(r *CheckRun) string {
	if r == nil {
		return "no run recorded on this machine"
	}
	phrase := r.Outcome
	if r.IntegrationFailure {
		phrase += ", without running " + strings.Join(r.Missing, ", ")
	}
	if r.Finished > 0 {
		phrase += " at " + time.Unix(int64(r.Finished), 0).Format("2006-01-02 15:04")
	}
	var how []string
	for _, part := range []string{r.Source, r.Disposition} {
		if part != "" {
			how = append(how, part)
		}
	}
	if len(how) > 0 {
		phrase += " (" + strings.Join(how, ", ") + ")"
	}
	if r.RefusedAfter > 0 {
		phrase += "; a later run was turned away with this one still holding the repository"
	}
	return phrase
}

// CheckPhrase names one declared check in one clause: what it binds to, how its
// proof stands, and what the last recorded execution was. A stale proof keeps
// running; an unobserved check is not a pass.
func CheckPhrase(c CheckRow) string {
	phrase := c.ID + " (" + c.Binding + "): "
	switch c.Validation {
	case ValidationNone:
		phrase += "never replayed here"
	case CheckStale:
		phrase += "replayed, then " + strings.Join(c.Changed, " and ") + " changed"
	default:
		phrase += "replayed and unchanged since"
	}
	if c.Execution == ExecutionUnobserved {
		phrase += "; no execution recorded here"
		return phrase
	}
	phrase += "; last " + c.Execution
	if c.ExecutedAt > 0 {
		phrase += " at " + time.Unix(int64(c.ExecutedAt), 0).Format("2006-01-02 15:04")
	}
	return phrase
}
