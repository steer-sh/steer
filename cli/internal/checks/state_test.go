package checks

import (
	"testing"
	"time"
)

// Each harness's Stop runner is observed through its own runs. A run from
// another source belongs to no runner, and a refused run recorded no work.
func TestStopObservedNamesEachHarnessOwnRuns(t *testing.T) {
	observed := stopObserved([]OracleRun{
		{Source: SourceStop, Harness: "claude-code", Finished: time.Unix(100, 0)},
		{Source: SourceStop, Harness: "codex", Finished: time.Unix(200, 0)},
		{Source: SourceStop, Harness: "claude-code", Finished: time.Unix(300, 0)},
		{Source: SourceStop, Harness: "codex", Finished: time.Unix(400, 0), ConcurrentWith: "held"},
		{Source: SourceRun, Harness: "claude-code", Finished: time.Unix(500, 0)},
	})
	if got := observed["claude-code"]; !got.Equal(time.Unix(300, 0)) {
		t.Errorf("claude-code observed %v; want its own newest Stop run, not the manual one", got)
	}
	if got := observed["codex"]; !got.Equal(time.Unix(200, 0)) {
		t.Errorf("codex observed %v; want the run it made, not the one it was refused", got)
	}
	if _, ok := observed["pi"]; ok {
		t.Error("a harness that ran nothing was reported as observed")
	}
}

// A run the repository lock turned away executed nothing, so the newest run
// that did the work is the one reported, with the refusal beside it.
func TestLatestRunsReportsTheHolderRatherThanTheRefusal(t *testing.T) {
	history := []OracleRun{
		{RunID: "first", Outcome: OutcomeFailed},
		{RunID: "held", Outcome: OutcomePassed},
		{RunID: "refused", Outcome: OutcomeNotExecuted, ConcurrentWith: "held"},
	}
	executed, refused := LatestRuns(history)
	if executed == nil || executed.RunID != "held" {
		t.Fatalf("executed %+v; want the run that held the repository", executed)
	}
	if refused == nil || refused.RunID != "refused" {
		t.Fatalf("refused %+v; want the run turned away after it", refused)
	}

	executed, refused = LatestRuns(history[2:])
	if executed != nil {
		t.Fatalf("executed %+v; want none when every run on record was refused", executed)
	}
	if refused == nil || refused.RunID != "refused" {
		t.Fatalf("refused %+v; want the refusal still reported", refused)
	}

	if executed, refused := LatestRuns(nil); executed != nil || refused != nil {
		t.Fatalf("empty history reported %+v / %+v", executed, refused)
	}
}

// StateOf joins one repository's declarations, receipts, live executions and
// oracle runs, and reports a declaration nothing validated as never validated
// with no execution rather than as a pass.
func TestStateOfJoinsDeclarationsReceiptsAndExecutions(t *testing.T) {
	root, opts := oracleRepo(t, nil, "")
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if _, err := Add(root, noCount); err != nil {
		t.Fatalf("Add: %v", err)
	}
	m, _ := Load(root)
	m.Checks = append(m.Checks, Declaration{ID: "unproven", Kind: KindScript, Script: ".steer/checks/no-count.sh"})
	if err := Save(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(root, AdoptOptions{SteerBin: opts.SteerBin, Timeout: opts.Timeout}); err == nil {
		t.Fatal("adoption passed with a declared check that was never validated")
	}
	run, _, err := RunOracle(root, opts)
	if err != nil {
		t.Fatalf("RunOracle: %v", err)
	}

	st, err := StateOf(root)
	if err != nil {
		t.Fatalf("StateOf: %v", err)
	}
	identity, _ := RepoIdentity(root)
	if st.Repo != identity || !st.Manifest || st.Adopted {
		t.Fatalf("state %+v; want the resolved root, a manifest and no adoption", st)
	}
	if len(st.Checks) != 2 {
		t.Fatalf("state carries %d checks; want both declarations", len(st.Checks))
	}
	proven, unproven := st.Checks[0], st.Checks[1]
	if proven.Freshness.State != FreshCurrent || proven.Execution == nil || proven.Execution.Outcome != OutcomePassed {
		t.Fatalf("no-count %+v; want a current receipt and a passing execution", proven)
	}
	if unproven.Freshness.State != FreshNone || unproven.Execution == nil {
		t.Fatalf("unproven %+v; want no receipt and an execution record from the run", unproven)
	}
	if st.Latest == nil || st.Latest.RunID != run.RunID || st.Refused != nil {
		t.Fatalf("latest run %+v refused %+v; want the run just made", st.Latest, st.Refused)
	}
}
