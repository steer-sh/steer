package firingaudit

import (
	"sort"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
)

// CheckCase is one Stop block assembled for reading: the run that blocked, the
// records under it, the failures a reading judges, and what the records show next.
type CheckCase struct {
	Run checks.OracleRun `json:"run"`
	// Executions are every record under the run, the parent's not-executed
	// records for checks the run never reached included.
	Executions []checks.Execution   `json:"executions,omitempty"`
	Failures   []CheckFailure       `json:"failures"`
	Followup   checks.BlockFollowup `json:"-"`
}

// CheckFailure is one failure a blocked run reported, in the kinds a check
// receipt names: adjudication.FailureCheck, FailureOracle or FailureIntegration.
type CheckFailure struct {
	Kind  string `json:"kind"`
	Check string `json:"check,omitempty"`
	// Execution is the record that reported a check failure. The oracle's and the
	// integration failure have none of their own.
	Execution *checks.Execution `json:"execution,omitempty"`
	// Required reports that a reading of the run must judge this failure. The
	// oracle's own failure is optional beside a failing check, because check.sh
	// exits non-zero for that check and may report nothing else.
	Required bool `json:"required"`
}

// failedOutcomes are the execution outcomes that report a failure. A check the
// run never reached, or one that skipped itself, asserted nothing.
var failedOutcomes = map[string]bool{
	checks.OutcomeFailed:      true,
	checks.OutcomeError:       true,
	checks.OutcomeTimeout:     true,
	checks.OutcomeUnavailable: true,
}

// CheckFailures lists the failures a blocked run reported, from its own record
// and the executions under it: each failing check in record order, then check.sh
// itself when it failed, then the integration failure when check.sh passed without
// running a declared check.
func CheckFailures(run checks.OracleRun, executions []checks.Execution) []CheckFailure {
	var out []CheckFailure
	seen := map[string]bool{}
	for i, e := range executions {
		if e.RunID != run.RunID || !failedOutcomes[e.Outcome] || seen[e.CheckID] {
			continue
		}
		seen[e.CheckID] = true
		out = append(out, CheckFailure{Kind: adjudication.FailureCheck, Check: e.CheckID,
			Execution: &executions[i], Required: true})
	}
	if run.Outcome == checks.OutcomeFailed {
		out = append(out, CheckFailure{Kind: adjudication.FailureOracle, Required: len(out) == 0})
	}
	if run.IntegrationFailure {
		out = append(out, CheckFailure{Kind: adjudication.FailureIntegration, Required: true})
	}
	return out
}

// CheckCases assembles every Stop block in live evidence read across
// repositories, oldest first.
func CheckCases(ev checks.LiveEvidence) []CheckCase {
	type runKey struct{ repo, run string }
	byRun := map[runKey][]checks.Execution{}
	for _, e := range ev.Executions {
		k := runKey{e.Repo, e.RunID}
		byRun[k] = append(byRun[k], e)
	}
	var out []CheckCase
	for _, f := range checks.LiveBlockFollowups(ev) {
		executions := byRun[runKey{f.Block.Repo, f.Block.RunID}]
		out = append(out, CheckCase{
			Run:        f.Block,
			Executions: executions,
			Failures:   CheckFailures(f.Block, executions),
			Followup:   f,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Run.Started.Before(out[j].Run.Started)
	})
	return out
}

// LoadCheckCases assembles every Stop block recorded in this machine's live check
// state. The state is machine-local and partial, like every evidence component.
func LoadCheckCases() ([]CheckCase, error) {
	ev, err := checks.ReadLiveEvidence()
	return CheckCases(ev), err
}

// FindCheckCase returns the case of the named run, and whether one exists.
func FindCheckCase(cases []CheckCase, runID string) (CheckCase, bool) {
	if runID == "" {
		return CheckCase{}, false
	}
	for _, c := range cases {
		if c.Run.RunID == runID {
			return c, true
		}
	}
	return CheckCase{}, false
}
