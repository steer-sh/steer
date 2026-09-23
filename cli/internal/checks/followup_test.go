package checks

import (
	"testing"
	"time"
)

func at(minute int) time.Time { return time.Date(2026, 9, 21, 2, minute, 0, 0, time.UTC) }

func stopRun(id, session, disposition, tree string, minute int) OracleRun {
	return OracleRun{RunID: id, Source: SourceStop, Harness: "codex", Session: session, Outcome: OutcomeFailed,
		Disposition: disposition, Tree: tree, Started: at(minute), Finished: at(minute).Add(time.Second)}
}

func execution(run, check, outcome, digest string, minute int) Execution {
	return Execution{RunID: run, CheckID: check, Outcome: outcome, DetectorDigest: digest, Started: at(minute)}
}

// The one real block's shape: the session never stopped again, another
// session's run passed next with the detector unchanged, and the block's row
// predates the tree field. The follow-up says so instead of reading a fix.
func TestFollowupOfABlockTheSessionNeverResumed(t *testing.T) {
	block := stopRun("b", "s1", DispositionBlock, "", 46)
	refused := OracleRun{RunID: "c", Source: SourceStop, Outcome: OutcomeNotExecuted, ConcurrentWith: "x", Started: at(47)}
	other := stopRun("o", "s2", DispositionAllow, "t9", 49)
	other.Outcome = OutcomePassed
	got := blockFollowups([]OracleRun{block, refused, other}, []Execution{
		execution("b", "go", OutcomeFailed, "d1", 46),
		execution("b", "revert-test", OutcomeNotExecuted, "r1", 46),
		execution("b", "fmt", OutcomePassed, "f1", 46),
		execution("o", "revert-test", OutcomePassed, "r1", 49),
	})
	if len(got) != 1 {
		t.Fatalf("followups %d; want 1", len(got))
	}
	f := got[0]
	if f.NextStop != nil || f.SessionTree != FollowUnknown {
		t.Errorf("next stop %+v, tree %s; want none and unknown", f.NextStop, f.SessionTree)
	}
	if f.NextRun == nil || f.NextRun.RunID != "o" {
		t.Errorf("next run %+v; want the executed run o, not the refused one", f.NextRun)
	}
	if len(f.Checks) != 2 || f.Checks[0].Blocked.CheckID != "go" || f.Checks[1].Blocked.CheckID != "revert-test" {
		t.Fatalf("checks %+v; want go and revert-test, not the passing fmt", f.Checks)
	}
	if c := f.Checks[0]; c.Next != nil || c.Detector != FollowUnknown {
		t.Errorf("go followup %+v; want no later result", c)
	}
	if c := f.Checks[1]; c.NextPass == nil || c.NextPass.RunID != "o" || c.Detector != FollowUnchanged {
		t.Errorf("revert-test followup %+v; want an unchanged detector at run o's pass", c)
	}
}

// A session that edits and passes shows a changed tree; a check that failed
// again before it passed keeps the failure as its next result and compares
// the detector at the pass.
func TestFollowupOfABlockTheSessionFixed(t *testing.T) {
	block := stopRun("b", "s1", DispositionBlock, "t1", 10)
	yield := stopRun("y", "s1", DispositionYield, "t1", 12)
	fixed := stopRun("f", "s1", DispositionAllow, "t2", 14)
	fixed.Outcome = OutcomePassed
	runs := []OracleRun{block, yield, fixed}
	execs := []Execution{
		execution("b", "a", OutcomeFailed, "d1", 10),
		execution("y", "a", OutcomeFailed, "d1", 12),
		execution("f", "a", OutcomePassed, "d2", 14),
	}
	got := blockFollowups(runs, execs)
	if len(got) != 1 {
		t.Fatalf("followups %d; want 1, a yield is no block", len(got))
	}
	f := got[0]
	if f.NextStop == nil || f.NextStop.RunID != "y" || f.SessionTree != FollowUnchanged {
		t.Errorf("next stop %+v tree %s; want the yield on an unchanged tree", f.NextStop, f.SessionTree)
	}
	c := f.Checks[0]
	if c.Next == nil || c.Next.RunID != "y" || c.NextPass == nil || c.NextPass.RunID != "f" || c.Detector != FollowChanged {
		t.Errorf("check followup %+v; want next y, pass f, detector changed", c)
	}

	block.Tree, runs[0] = "t1", block
	runs[1] = fixed
	if f := blockFollowups(runs[:2], execs)[0]; f.SessionTree != FollowChanged {
		t.Errorf("tree %s; want changed when the next stop judged t2", f.SessionTree)
	}
}
