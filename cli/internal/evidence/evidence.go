// Package evidence gathers every corpus a scoring pass reads into one value. The
// scoring engines take that value and know no file layout; this package knows the
// layout and no statistics. Cadence, status and the intervention commands all call it,
// so a scored verdict never depends on which entry point asked for it.
package evidence

import (
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/sessions"
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
	// AllProjects scans every project rather than the working directory's.
	AllProjects bool
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
			WindowDays:  opts.WindowDays,
			AllProjects: opts.AllProjects,
		})
	} else {
		ev.Sessions, _ = sessions.Load(dir)
	}

	ev.Firings, ev.AuditPresent = mine.LoadFirings(GuardStores()...)
	ev.CheckRuns, ev.CheckRepos = CheckRuns()
	return ev
}

// CheckRuns reads this machine's live check evidence as the observation's execution
// stream, and the repositories that hold any check evidence here. A failed execution
// is Blocked when its oracle run's disposition was block.
func CheckRuns() ([]mine.CheckRun, map[string]bool) {
	live, _ := checks.ReadLiveEvidence()
	return checkRunsOf(live), live.Repos
}

func checkRunsOf(live checks.LiveEvidence) []mine.CheckRun {
	blocking := map[string]bool{}
	for _, r := range live.Runs {
		if r.Disposition == checks.DispositionBlock {
			blocking[r.RunID] = true
		}
	}
	runs := make([]mine.CheckRun, 0, len(live.Executions))
	for _, e := range live.Executions {
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
