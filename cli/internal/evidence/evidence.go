// Package evidence gathers every corpus a scoring pass reads into one value. The
// scoring engines take that value and know no file layout; this package knows the
// layout and no statistics. Cadence, status and the intervention commands all call it,
// so a scored verdict never depends on which entry point asked for it.
package evidence

import (
	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/sessions"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Options name the corpora to read and how far back to read them.
type Options struct {
	// GateStateDir holds interventions.jsonl and the ledger shards.
	GateStateDir string
	// Ledger overrides the shard glob with one named file.
	Ledger string
	// WindowDays bounds a transcript rebuild; 0 skips the rebuild and reads the
	// stored snapshots alone.
	WindowDays int
	// ScanScope selects which sessions a rebuild reads. The zero value reads
	// every project.
	ScanScope report.Scope
	// Rebuild re-reads the harnesses' transcripts and merges the result into the
	// snapshot store. A read-only caller leaves it false.
	Rebuild bool
}

// Gather reads the chain corpus, the session corpus, the guard audit stream and the
// check execution stream. Every
// source fails soft: a missing corpus yields an empty section, never an error.
func Gather(opts Options) mine.EvidenceSet {
	var ev mine.EvidenceSet

	rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
	ev.Chains = mine.BuildChains(rows, false)

	dir := sessions.StateDir()
	if opts.Rebuild {
		ev.Sessions, _ = sessions.Refresh(dir, gate.HostID(), sessions.Options{
			WindowDays: opts.WindowDays,
			Scope:      opts.ScanScope,
		})
	} else {
		ev.Sessions, _ = sessions.Load(dir)
	}

	firings, present := mine.LoadStoreFirings(AuditStores()...)
	ev.Firings, ev.AuditPresent = WithoutExcluded(firings, Exclusions()), present
	ev.CheckRuns, ev.CheckRepos = CheckRuns(ExcludedRuns())

	records, _ := mine.Load(opts.GateStateDir)
	ev.FixtureHost = gate.HostID()
	ev.FixtureProjectRoot = ProjectRoot()
	ev.FixtureReceipts = FixtureReceipts(records, ev.FixtureHost, ev.FixtureProjectRoot)
	return ev
}

// Exclusions reads the rows this machine's exclusion stream retires, keyed by row
// digest. An unreadable stream retires nothing.
func Exclusions() map[string]adjudication.Exclusion {
	entries, err := adjudication.LoadExclusions(adjudication.Dir())
	if err != nil {
		return nil
	}
	return adjudication.Excluded(entries)
}

// ExcludedRuns reads the check blocks this machine's exclusion stream retires,
// keyed by run. An unreadable stream retires nothing.
func ExcludedRuns() map[string]adjudication.Exclusion {
	entries, err := adjudication.LoadExclusions(adjudication.Dir())
	if err != nil {
		return nil
	}
	return adjudication.ExcludedRuns(entries)
}

// WithoutExcluded drops the firings an exclusion retires. A firing carrying no row
// digest is kept, because nothing can name it.
func WithoutExcluded(firings []mine.Firing, excluded map[string]adjudication.Exclusion) []mine.Firing {
	if len(excluded) == 0 {
		return firings
	}
	out := make([]mine.Firing, 0, len(firings))
	for _, f := range firings {
		if _, ok := excluded[f.RowDigest]; ok && f.RowDigest != "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// CheckRuns reads this machine's live check evidence as the observation's execution
// stream, and the repositories that hold any check evidence here. A failed execution
// is Blocked when its oracle run's disposition was block. The executions of a block
// an exclusion retires are left out; the records themselves stay.
func CheckRuns(excludedRuns map[string]adjudication.Exclusion) ([]mine.CheckRun, map[string]bool) {
	live, _ := checks.ReadLiveEvidence()
	return checkRunsOf(live, excludedRuns), live.Repos
}

func checkRunsOf(live checks.LiveEvidence, excludedRuns map[string]adjudication.Exclusion) []mine.CheckRun {
	blocking := map[string]bool{}
	for _, r := range live.Runs {
		if r.Disposition == checks.DispositionBlock {
			blocking[r.RunID] = true
		}
	}
	runs := make([]mine.CheckRun, 0, len(live.Executions))
	for _, e := range live.Executions {
		if _, retired := excludedRuns[e.RunID]; retired && e.RunID != "" {
			continue
		}
		runs = append(runs, mine.CheckRun{
			Repo:           e.Repo,
			CheckID:        e.CheckID,
			Harness:        e.Harness,
			SessionID:      e.Session,
			Outcome:        e.Outcome,
			DetectorDigest: e.DetectorDigest,
			TS:             float64(e.Started.UnixNano()) / 1e9,
			Blocked:        e.Outcome == checks.OutcomeFailed && e.RunID != "" && blocking[e.RunID],
		})
	}
	return runs
}

// GuardStores names the guard store directories whose audit streams carry firings. The
// two scopes resolve to one directory on a machine with only global guards, which the
// reader deduplicates.
func GuardStores() []string {
	var dirs []string
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if dir, err := claude.GetHooksDir(scope); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// AuditStores names each guard store with the scope it resolved under, so a firing
// is attributed to the store that wrote it. A project scope that resolves to the
// global store's directory is read as the global store.
func AuditStores() []mine.AuditStore {
	global, globalErr := claude.GetHooksDir(claude.GlobalScope)
	var stores []mine.AuditStore
	if project, err := claude.GetHooksDir(claude.ProjectScope); err == nil &&
		(globalErr != nil || project != global) {
		stores = append(stores, mine.AuditStore{Scope: claude.ProjectScope, Dir: project})
	}
	if globalErr == nil {
		stores = append(stores, mine.AuditStore{Scope: claude.GlobalScope, Dir: global})
	}
	return stores
}

// FixtureReceipts reads the fixture state behind every guard the records name, keyed
// by the store this machine resolved. host and projectRoot are the identities that
// resolved them, and it reads a store only for a key some record can be attributed
// to here; the per-record ownership test is the reader's. A missing store or hook
// yields no entry, and an unreadable one an entry naming the failure.
func FixtureReceipts(records []mine.Record, host, projectRoot string) map[mine.FixtureKey]mine.FixtureReceipt {
	out := map[mine.FixtureKey]mine.FixtureReceipt{}
	seen := map[mine.FixtureKey]bool{}
	for _, record := range records {
		fix, bound := mine.GuardFixOf(record)
		if !bound || fix.Scope == "" {
			continue
		}
		if fix.Host != "" && host != "" && fix.Host != host && !gate.IsFormerHost(fix.Host) {
			continue
		}
		if fix.Scope == steerpath.ProjectScope && (fix.Repo == "" || fix.Repo != projectRoot) {
			continue
		}
		key := mine.FixtureKey{Scope: fix.Scope, HookID: fix.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		if receipt, ok := GuardFixtureReceipt(fix.Scope, fix.ID); ok {
			out[key] = receipt
		}
	}
	return out
}

// GuardFixtureReceipt reads one guard's fixture state in the store this machine
// resolves for scope. ok is false when the store or the guard is not there; an
// unreadable one is a receipt naming the failure.
func GuardFixtureReceipt(scope, hook string) (mine.FixtureReceipt, bool) {
	snapshot, err := claude.ReadHookFixtureSnapshot(scope, hook)
	switch {
	case err != nil:
		return mine.FixtureReceipt{
			Applicable: true,
			State:      mine.FixtureUnavailable,
			Reason:     err.Error(),
		}, true
	case snapshot == nil:
		return mine.FixtureReceipt{}, false
	}
	return receiptOf(snapshot), true
}

// receiptOf reads one snapshot into the reported state. Precedence is attribution,
// then a retained pass, then the master identity that pass named, then freshness:
// a manifest keeps the digests of a run whose outcome it no longer holds, so a
// cleared Tested is never read back as a failure or as a pass.
func receiptOf(s *claude.FixtureSnapshot) mine.FixtureReceipt {
	r := mine.FixtureReceipt{Applicable: true}
	if s.Binding == nil || !s.Binding.Tested {
		r.State = mine.FixtureNone
		return r
	}
	cases, shadowed := s.Binding.Cases, s.Binding.Shadowed
	r.Cases, r.Shadowed, r.VerifiedAt = &cases, &shadowed, s.Binding.VerifiedAt
	if s.NoMasterIdentity {
		r.State = mine.FixtureUnavailable
		r.Reason = "the retained pass recorded no master identity, so it validates the guard under no master"
		return r
	}
	r.Changed = append(append([]string{}, s.Changed...), s.Unavailable...)
	if len(r.Changed) == 0 {
		r.State = mine.FixtureCurrent
		return r
	}
	r.State = mine.FixtureStale
	r.Reason = s.Reason
	return r
}

// ProjectRoot is the checkout whose project guard store this command resolves,
// canonicalized the way a registration freezes it. Outside a repository it is
// empty, and a project-scope record then reaches no receipt here.
func ProjectRoot() string {
	root, err := checks.RepoRootOf(".")
	if err != nil {
		return ""
	}
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		return ""
	}
	return identity
}
