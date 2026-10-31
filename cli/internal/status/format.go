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
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
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
	s := fmt.Sprintf("%d of %d counted", c.Eligible, c.Total)
	extra := []string{}
	if c.Ineligible > 0 {
		extra = append(extra, fmt.Sprintf("%d out of scope", c.Ineligible))
	}
	if c.UnknownScope > 0 {
		extra = append(extra, fmt.Sprintf("%d with no repository recorded", c.UnknownScope))
	}
	if c.LedgerOverdue > 0 {
		extra = append(extra, fmt.Sprintf("%d with no outcome read", c.LedgerOverdue))
	}
	if c.LedgerMissed > 0 {
		extra = append(extra, fmt.Sprintf("%d with no decision found", c.LedgerMissed))
	}
	// A map written before the split carries only the total.
	if rest := c.UnknownEvidence - c.Pending - c.LedgerOverdue - c.LedgerMissed; rest > 0 {
		extra = append(extra, fmt.Sprintf("%d with no decision recorded", rest))
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

// TrustPhrase names a harness's posture on a registration, "" when nothing needs
// saying. Registration alone does not prove enforcement on a harness that reviews hook
// trust itself.
func TrustPhrase(trust string) string {
	switch trust {
	case TrustRecordPresent:
		return "approval could not be compared"
	case TrustModified:
		return "approval needs checking"
	case TrustDisabled:
		return "turned off"
	case TrustHooksOff:
		return "all Codex hooks are turned off"
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
		return fmt.Sprintf("runtime %s is older than this binary's %s — run: %s",
			m.Runtime, m.Expected, RebuildCommand(m.Scope))
	case MasterUnknown:
		return "runtime has no version (unstamped) — run: " + RebuildCommand(m.Scope)
	case MasterDetached:
		if m.Path == "" {
			return "runs none of this scope's guards: no runner is registered — run: " + SyncCommand(m.Scope)
		}
		return fmt.Sprintf("runs none of this scope's guards: the registration names %s, not %s — run: %s",
			m.Path, m.Store, SyncCommand(m.Scope))
	}
	return ""
}

// RebuildCommand writes a scope's master again from the guards it holds, and
// repoints every registration at it.
func RebuildCommand(scope string) string {
	return "steer hooks rebuild --scope " + scope
}

// StaleScopes names each scope with a registered master that needs a rebuild,
// once, in the order the rows list them.
func StaleScopes(rows []MasterRuntime) []string {
	var scopes []string
	seen := map[string]bool{}
	for _, m := range rows {
		if m.Stale() && !seen[m.Scope] {
			seen[m.Scope] = true
			scopes = append(scopes, m.Scope)
		}
	}
	return scopes
}

// SyncCommand registers a scope's existing master and rules in every installed
// harness without regenerating the master.
func SyncCommand(scope string) string {
	return "steer hooks sync --scope " + scope
}

// RulesRepairCommand rewrites every installed harness's rules section in a scope.
func RulesRepairCommand(scope string) string {
	return SyncCommand(scope)
}

// MasterSourcePhrase names what a registered guard runner runs against the
// store's own guard files, "" when the reading adds nothing. It is a reading and
// not a repair: which artifact the operator wants running is not a fact about
// the bytes.
func MasterSourcePhrase(m MasterRuntime) string {
	if m.Detached() {
		if m.StoreSourcesDrifted() {
			return "the runner built from these guard files does not carry " +
				strings.Join(m.StoreSources.Drifted(), ", ") + " as the file holds it"
		}
		return ""
	}
	if m.Sources == nil || m.State == MasterNotRegistered {
		return ""
	}
	switch m.Sources.State {
	case claude.ComparisonUnavailable:
		return "guard source could not be read: " + m.Sources.Reason
	case claude.ComparisonNoMaster:
		return "no guard runner file to read"
	}
	drifted := m.Sources.Drifted()
	if len(drifted) == 0 {
		return ""
	}
	return fmt.Sprintf("it runs different source for %s than the guard file holds",
		strings.Join(drifted, ", "))
}

// DriftedMasters summarizes the rows whose registered runner carries different
// guard source, in the shape DetachedMasters uses.
func DriftedMasters(rows []MasterRuntime) (key string, labels, scopes []string) {
	var parts []string
	seen := map[string]bool{}
	for _, m := range rows {
		if !m.SourcesDrifted() {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s/%s:%s", m.Harness, m.Scope,
			strings.Join(m.Sources.Drifted(), ",")))
		labels = append(labels, fmt.Sprintf("%s, %s scope", HarnessLabel(m.Harness), m.Scope))
		if !seen[m.Scope] {
			seen[m.Scope] = true
			scopes = append(scopes, m.Scope)
		}
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels, scopes
}

// DetachedRepair names the command that registers a scope's guards again. Sync
// refuses a store whose own runner does not carry its guards, so a scope holding
// one needs the rebuild that writes and registers in one operation.
func DetachedRepair(rows []MasterRuntime, scope string) string {
	for _, m := range rows {
		if m.Scope == scope && m.Detached() && m.StoreSourcesDrifted() {
			return RebuildCommand(scope)
		}
	}
	return SyncCommand(scope)
}

// DetachedMasters summarizes the detached rows for a one-line notice, in the shape
// RulesDrift uses: a key that changes whenever the set does, one label per row,
// and each scope to sync, once.
func DetachedMasters(rows []MasterRuntime) (key string, labels, scopes []string) {
	var parts []string
	seen := map[string]bool{}
	for _, m := range rows {
		if !m.Detached() {
			continue
		}
		store := m.Store
		if abs, err := filepath.Abs(store); err == nil {
			store = abs
		}
		parts = append(parts, fmt.Sprintf("%s/%s:%s:%s", m.Harness, m.Scope, store, m.Path))
		labels = append(labels, fmt.Sprintf("%s, %s scope", HarnessLabel(m.Harness), m.Scope))
		if !seen[m.Scope] {
			seen[m.Scope] = true
			scopes = append(scopes, m.Scope)
		}
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels, scopes
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

// ObservedPhrase names when this channel last recorded its own work: a review for
// the commit gate, a run for the check runner. An empty string means the channel
// writes no corpus, so nothing about it is observable. "never observed" is not
// proof of breakage: nobody may have attempted the action the channel fires on.
func ObservedPhrase(a ScopeArming) string {
	if a.ObservedCorpus == "" {
		return ""
	}
	if a.Observed == 0 {
		return "never observed"
	}
	what := "last recorded"
	switch a.ObservedCorpus {
	case ledgerCorpus:
		what = "last review"
	case checkCorpus:
		what = "last check run"
	}
	return what + " " + time.Unix(int64(a.Observed), 0).Format("2006-01-02 15:04")
}

// RunnerUnapproved reports a registered runner the harness is known not to have
// approved, so it runs no guard there while the rule's text still reaches the agent.
func (m MasterRuntime) RunnerUnapproved() bool {
	return m.State != MasterNotRegistered && !m.Detached() && m.Trust == TrustMissing
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
	if m.State == MasterNotRegistered || m.Detached() {
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
		phrase += ", verification incomplete"
		if len(r.Missing) > 0 {
			phrase += ", without running " + strings.Join(r.Missing, ", ")
		}
	}
	if how := checkRunSource(r.Source); how != "" {
		phrase += " " + how
	}
	if r.Finished > 0 {
		phrase += " on " + time.Unix(int64(r.Finished), 0).Format("2006-01-02 15:04")
	}
	switch r.Disposition {
	case checks.DispositionBlock:
		phrase += "; it blocked the turn"
	case checks.DispositionYield:
		phrase += "; it failed again on an unchanged tree, so the turn ended without another block"
	}
	if r.RefusedAfter > 0 {
		phrase += "; a later run was turned away with this one still holding the repository"
	}
	return phrase
}

// checkRunSource names what started a check.sh run, "" for a source a reader
// has no phrase for.
func checkRunSource(source string) string {
	switch source {
	case checks.SourceStop:
		return "at the end of a turn"
	case checks.SourceRun:
		return "from steer check run"
	case checks.SourceAdopt:
		return "during steer check adopt"
	}
	return ""
}

// CheckPhrase names one declared check in one clause: what it binds to, how its
// proof stands, and what the last recorded execution was. A stale proof keeps
// running; an unobserved check is not a pass.
func CheckPhrase(c CheckRow) string {
	return c.ID + " (" + c.Binding + "): " + CheckStatePhrase(c)
}

// CheckStatePhrase is CheckPhrase without the check's name: how its proof stands
// and what the last recorded execution was.
func CheckStatePhrase(c CheckRow) string {
	phrase := ""
	switch c.Validation {
	case ValidationNone:
		phrase += "never tested here"
	case CheckStale:
		phrase += "tested, then " + strings.Join(c.Changed, " and ") + " changed"
	default:
		phrase += "tested and unchanged since"
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

// GuardInterpreter is the interpreter every registered guard runner starts under.
const GuardInterpreter = "python3"

// InterpreterReading is whether this process finds the guard interpreter on PATH. The
// harness runs a hook under its own PATH, so a found interpreter proves nothing about
// the hook and only a missing one is worth reporting.
type InterpreterReading struct {
	Command string `json:"command"`
	Found   bool   `json:"found"`
}

// ReadInterpreter looks GuardInterpreter up with lookPath.
func ReadInterpreter(lookPath func(string) (string, error)) InterpreterReading {
	_, err := lookPath(GuardInterpreter)
	return InterpreterReading{Command: GuardInterpreter, Found: err == nil}
}

// RunnersWithoutInterpreter lists the registered guard runners when the interpreter
// is missing, and nil when it is found or none is registered. A runner whose
// interpreter is missing allows every call, so its guards enforce nothing.
func RunnersWithoutInterpreter(masters []MasterRuntime, interp InterpreterReading) []MasterRuntime {
	var registered []MasterRuntime
	for _, m := range masters {
		if m.Path != "" {
			registered = append(registered, m)
		}
	}
	if len(registered) == 0 || interp.Found {
		return nil
	}
	return registered
}
