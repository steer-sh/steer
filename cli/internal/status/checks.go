// The check tier's share of the snapshot: what this repository declares, whether
// this machine authorized its oracle to run at turn end, and what the last run
// recorded. Registration, adoption and observed execution are three facts, and
// this file keeps them apart.

package status

import (
	"os"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// checkCorpus is the stream the Stop runner writes: one record per oracle run.
const checkCorpus = "check-runs"

// CheckStale is a declared check whose receipt no longer covers the detector as
// it is now. It keeps running: the label reports what moved, not a repair.
const CheckStale = checks.FreshStale

// ValidationNone is a declared check this machine never replayed. The other two
// states are checks.FreshCurrent and checks.FreshStale, and a stale check keeps
// running: the label reports what moved since its receipt, not a repair.
const ValidationNone = checks.FreshNone

// ExecutionUnobserved is a check no run on this machine recorded. It is never a
// pass: an aggregate script outcome attributes no result to one assertion.
const ExecutionUnobserved = ""

// CheckRun is one run of the repository's oracle. Missing names the declared
// checks no executor reported; after a passing script they are IntegrationFailure,
// otherwise checks the run never reached. RefusedAfter carries the run the
// repository lock turned away after this one, which executed nothing of its own.
type CheckRun struct {
	RunID              string   `json:"run_id"`
	Source             string   `json:"source"`
	Harness            string   `json:"harness,omitempty"`
	Outcome            string   `json:"outcome"`
	Disposition        string   `json:"disposition,omitempty"`
	Finished           float64  `json:"finished,omitempty"`
	Missing            []string `json:"missing,omitempty"`
	IntegrationFailure bool     `json:"integration_failure,omitempty"`
	RefusedAfter       float64  `json:"refused_after,omitempty"`
}

// CheckRow is one declared check: its binding, how its latest receipt relates to
// the check as it is now, and the last execution recorded here.
type CheckRow struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Binding string `json:"binding"`
	// Validation is current, stale or none, with Changed naming what moved since
	// the receipt Receipt identifies.
	Validation  string   `json:"validation"`
	Changed     []string `json:"changed,omitempty"`
	Receipt     string   `json:"receipt,omitempty"`
	ValidatedAt float64  `json:"validated_at,omitempty"`
	// Execution is the outcome of the newest execution record for this check,
	// empty when this machine holds none.
	Execution    string  `json:"execution,omitempty"`
	ExecutedAt   float64 `json:"executed_at,omitempty"`
	ExecutionRun string  `json:"execution_run,omitempty"`
}

// Validated reports a check whose receipt covers the detector as it is now.
func (c CheckRow) Validated() bool { return c.Validation == checks.FreshCurrent }

// Passed reports a check whose newest recorded execution was a pass. An
// unobserved check is not one.
func (c CheckRow) Passed() bool {
	return c.Execution == checks.OutcomePassed || c.Execution == checks.OutcomePassedCached
}

// CheckTier is the repository's check tier on this machine. Repo is empty when
// the caller named no repository, which is the only case where the rest is not a
// reading. Error is why a store that IS there could not be read.
type CheckTier struct {
	Repo      string  `json:"repo,omitempty"`
	Manifest  bool    `json:"manifest"`
	Adopted   bool    `json:"adopted"`
	AdoptedAt float64 `json:"adopted_at,omitempty"`
	// Runner is the Stop runner's registration per harness and scope, read the
	// same three ways as every other channel.
	Runner    []ScopeArming `json:"runner"`
	Checks    []CheckRow    `json:"checks"`
	LatestRun *CheckRun     `json:"latest_run"`
	Error     string        `json:"error,omitempty"`
}

// Armed reports a repository whose oracle runs at turn end: adopted here, with
// the runner registered in some harness.
func (c CheckTier) Armed() bool {
	if !c.Adopted {
		return false
	}
	for _, a := range c.Runner {
		if a.Registered {
			return true
		}
	}
	return false
}

// collectChecks reads the check tier of one repository. A root the caller left
// empty still reports the runner's registration, which is machine state: outside
// a repository there is no adoption and no declaration to read, and a registered
// runner is still registered.
func collectChecks(root string) CheckTier {
	c := CheckTier{Runner: []ScopeArming{}, Checks: []CheckRow{}}
	if root == "" {
		dir, _ := os.Getwd()
		c.Runner = runnerArming(dir, nil)
		return c
	}
	st, err := checks.StateOf(root)
	c.Repo, c.Manifest, c.Adopted = st.Repo, st.Manifest, st.Adopted
	if st.Adopted {
		c.AdoptedAt = checkTimeUnix(st.Adoption.At)
	}
	if err != nil {
		c.Error = err.Error()
	}
	c.Runner = runnerArming(root, st.StopObserved)
	for _, cs := range st.Checks {
		c.Checks = append(c.Checks, checkRow(cs))
	}
	c.LatestRun = CheckRunOf(st.Latest, st.Refused)
	return c
}

// checkRow renders one declared check's binding, receipt and last execution.
func checkRow(cs checks.CheckState) CheckRow {
	row := CheckRow{
		ID:         cs.Declaration.ID,
		Kind:       cs.Declaration.Kind,
		Binding:    BindingPhrase(cs.Declaration),
		Validation: cs.Freshness.State,
		Changed:    cs.Freshness.Changed,
	}
	if r := cs.Freshness.Receipt; r != nil {
		row.Receipt, row.ValidatedAt = r.RunID, checkTimeUnix(r.Finished)
	}
	if e := cs.Execution; e != nil {
		row.Execution, row.ExecutedAt, row.ExecutionRun = e.Outcome, checkTimeUnix(e.Finished), e.RunID
	}
	return row
}

// CheckRunOf carries the newest run that executed, with the time of a later
// refusal beside it rather than in place of it. checks.LatestRuns supplies the
// pair; a nil latest with a refusal means every run on record was turned away.
func CheckRunOf(latest, refused *checks.OracleRun) *CheckRun {
	if latest == nil {
		if refused == nil {
			return nil
		}
		// Every run on record was refused, so the holder's own outcome is not
		// here: the run it names finished on another machine or not at all.
		return &CheckRun{RunID: refused.RunID, Source: refused.Source, Harness: refused.Harness,
			Outcome: refused.Outcome, Disposition: refused.Disposition,
			Finished: checkTimeUnix(refused.Finished)}
	}
	run := &CheckRun{
		RunID:              latest.RunID,
		Source:             latest.Source,
		Harness:            latest.Harness,
		Outcome:            latest.Outcome,
		Disposition:        latest.Disposition,
		Finished:           checkTimeUnix(latest.Finished),
		Missing:            latest.Missing,
		IntegrationFailure: latest.IntegrationFailure,
	}
	if refused != nil {
		run.RefusedAfter = checkTimeUnix(refused.Finished)
	}
	return run
}

// runnerArming reads the Stop runner's registration in each harness and scope.
// Observed is the newest run that harness's own runner recorded here, so a
// manual run and another harness's run leave it alone.
func runnerArming(root string, observed map[string]time.Time) []ScopeArming {
	out := []ScopeArming{}
	for _, target := range checkscope.Harnesses {
		for _, scope := range checkscope.Scopes {
			a, err := checkscope.RunnerArming(target, scope, root)
			if err != nil {
				a = checkscope.Arming{}
			}
			row := ScopeArming{
				Harness:    string(target),
				Scope:      scope,
				Registered: a.Registered,
				Effective:  a.Registered && (a.Timeout == 0 || a.Timeout >= checks.RegisteredHookTimeout()),
				Timeout:    a.Timeout,
				Trust:      runnerApprovalTrust(target, a),
				Command:    a.Command,
			}
			out = append(out, withEvidence(row, checkCorpus, checkTimeUnix(observed[string(target)])))
		}
	}
	return out
}

// runnerApprovalTrust reads the check runner's approval where the harness reviews one.
func runnerApprovalTrust(target harness.ID, a checkscope.Arming) string {
	if a.Registered && a.Approval != "" {
		return approvalTrust(a.Approval)
	}
	return trustState(target, false, false)
}

// BindingPhrase names what a declaration binds to: the script Steer runs, or the
// package and test it reads a result for.
func BindingPhrase(d checks.Declaration) string {
	if d.Kind == checks.KindGoTest {
		return d.Package + " " + d.Test
	}
	return d.Script
}

// RunnerTimeoutStep reports a registration whose wall clock is shorter than a run
// is allowed to take, which stops the runner mid-script with no result.
func RunnerTimeoutStep(a ScopeArming) (Step, bool) {
	if !a.Registered || a.Timeout <= 0 || a.Timeout >= checks.RegisteredHookTimeout() {
		return Step{}, false
	}
	return Step{
		Title: "the registered wall clock is shorter than a run is allowed to take",
		Fix:   "steer check install --harness " + a.Harness + " --scope " + a.Scope,
	}, true
}

// checkTimeUnix converts a stored time, keeping zero as zero rather than the
// epoch seconds of the zero time.
func checkTimeUnix(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}
