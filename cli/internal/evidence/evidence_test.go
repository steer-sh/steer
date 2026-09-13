package evidence

import (
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
)

// A failed execution is Blocked only when its own oracle run blocked the turn.
func TestAFailedExecutionIsBlockedOnlyUnderABlockingRun(t *testing.T) {
	at := time.Unix(3000, 0)
	live := checks.LiveEvidence{
		Executions: []checks.Execution{
			{RunID: "r-block", CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
			{RunID: "r-block", CheckID: "b", Outcome: checks.OutcomeNotExecuted, Started: at},
			{RunID: "r-yield", CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
			{CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
		},
		Runs: []checks.OracleRun{
			{RunID: "r-block", Disposition: checks.DispositionBlock},
			{RunID: "r-yield", Disposition: checks.DispositionYield},
		},
	}
	runs := checkRunsOf(live)
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4", len(runs))
	}
	want := []bool{true, false, false, false}
	for i, r := range runs {
		if r.Blocked != want[i] {
			t.Errorf("run %d (%s under %q) blocked = %t, want %t", i, r.Outcome, live.Executions[i].RunID, r.Blocked, want[i])
		}
	}
	if runs[0].TS != 3000 {
		t.Errorf("TS = %v, want the start time in unix seconds", runs[0].TS)
	}
}
