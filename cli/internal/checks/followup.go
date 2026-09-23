package checks

// Comparison states for a follow-up. Unknown means a record needed for the
// comparison is missing or predates the field.
const (
	FollowChanged   = "changed"
	FollowUnchanged = "unchanged"
	FollowUnknown   = "unknown"
)

// BlockFollowup is what the records show after one Stop block. It describes
// what happened next; it does not say whether the block was correct.
type BlockFollowup struct {
	Block OracleRun
	// NextStop is the next Stop run from the block's harness and session.
	NextStop *OracleRun
	// SessionTree compares the tree the block judged with NextStop's.
	SessionTree string
	// NextRun is the repository's next executed run from any source.
	NextRun *OracleRun
	// Checks follows each declared check the block recorded as not passing.
	Checks []CheckFollowup
}

// CheckFollowup follows one check that did not pass in a blocked run.
type CheckFollowup struct {
	Blocked Execution
	// Next is the check's next execution record after the block, and NextPass
	// its first passing one.
	Next     *Execution
	NextPass *Execution
	// Detector compares the detector at the block with the one at NextPass:
	// changed means the detector changed before the recorded pass.
	Detector string
}

// BlockFollowups returns a follow-up for each recorded Stop block in the
// repository, oldest first.
func BlockFollowups(root string) ([]BlockFollowup, error) {
	runs, err := OracleRuns(root)
	if err != nil {
		return nil, err
	}
	executions, err := Executions(root)
	if err != nil {
		return nil, err
	}
	return blockFollowups(runs, executions), nil
}

// LiveBlockFollowups returns a follow-up for each Stop block in live evidence
// read across repositories. Each repository's records are followed apart from the
// others, so a next run is always one of the block's own repository; within one
// repository the order is the stream's, and repositories keep the order ev holds.
func LiveBlockFollowups(ev LiveEvidence) []BlockFollowup {
	var repos []string
	runs := map[string][]OracleRun{}
	executions := map[string][]Execution{}
	for _, r := range ev.Runs {
		if _, ok := runs[r.Repo]; !ok {
			repos = append(repos, r.Repo)
		}
		runs[r.Repo] = append(runs[r.Repo], r)
	}
	for _, e := range ev.Executions {
		executions[e.Repo] = append(executions[e.Repo], e)
	}
	var out []BlockFollowup
	for _, repo := range repos {
		out = append(out, blockFollowups(runs[repo], executions[repo])...)
	}
	return out
}

func blockFollowups(runs []OracleRun, executions []Execution) []BlockFollowup {
	var out []BlockFollowup
	for i, block := range runs {
		if block.Source != SourceStop || block.Disposition != DispositionBlock {
			continue
		}
		f := BlockFollowup{Block: block, SessionTree: FollowUnknown}
		for j := i + 1; j < len(runs); j++ {
			r := runs[j]
			if f.NextStop == nil && r.Source == SourceStop && r.Harness == block.Harness && r.Session == block.Session {
				f.NextStop = &runs[j]
			}
			if f.NextRun == nil && executed(r) {
				f.NextRun = &runs[j]
			}
		}
		if f.NextStop != nil {
			f.SessionTree = compare(block.Tree, f.NextStop.Tree)
		}
		for _, e := range executions {
			if e.RunID == block.RunID && !e.Passed() {
				f.Checks = append(f.Checks, followCheck(e, block, executions))
			}
		}
		out = append(out, f)
	}
	return out
}

func followCheck(blocked Execution, block OracleRun, executions []Execution) CheckFollowup {
	c := CheckFollowup{Blocked: blocked, Detector: FollowUnknown}
	for i, e := range executions {
		if e.CheckID != blocked.CheckID || e.RunID == block.RunID || !e.Started.After(block.Finished) {
			continue
		}
		if c.Next == nil {
			c.Next = &executions[i]
		}
		if e.Passed() {
			c.NextPass = &executions[i]
			c.Detector = compare(blocked.DetectorDigest, e.DetectorDigest)
			break
		}
	}
	return c
}

// executed reports a run that ran check.sh: not refused by the lock and not
// stopped before it started.
func executed(r OracleRun) bool {
	return r.ConcurrentWith == "" && r.Outcome != OutcomeNotExecuted
}

func compare(before, after string) string {
	switch {
	case before == "" || after == "":
		return FollowUnknown
	case before == after:
		return FollowUnchanged
	default:
		return FollowChanged
	}
}
