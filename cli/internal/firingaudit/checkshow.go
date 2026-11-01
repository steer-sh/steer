package firingaudit

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// CheckShow is one check block assembled for reading: the case, the transcript
// around the block, and the judgments already made of it, oldest first.
type CheckShow struct {
	Case     CheckCase                   `json:"case"`
	Followup Followup                    `json:"followup"`
	Reading  transcript.StopBlockReading `json:"reading"`
	Prior    []adjudication.Receipt      `json:"prior,omitempty"`
}

// Followup is what the records show after a block, for reading beside the
// transcript. It says what happened next, never whether the block was correct.
type Followup struct {
	// NextStop is the same session's next Stop run, and SessionTree whether the
	// tree it judged differs from the block's: a checks.Follow* state.
	NextStop    *RunFact `json:"next_stop,omitempty"`
	SessionTree string   `json:"session_tree"`
	// NextRun is the repository's next executed run from any source.
	NextRun *RunFact    `json:"next_run,omitempty"`
	Checks  []CheckFact `json:"checks,omitempty"`
}

// RunFact names one later run.
type RunFact struct {
	RunID       string `json:"run_id"`
	Source      string `json:"source"`
	Session     string `json:"session,omitempty"`
	Started     string `json:"started"`
	Outcome     string `json:"outcome"`
	Disposition string `json:"disposition,omitempty"`
}

// CheckFact follows one check that did not pass at the block: its next result,
// its next pass, and whether its detector changed before that pass.
type CheckFact struct {
	Check          string `json:"check"`
	Blocked        string `json:"blocked"`
	Next           string `json:"next,omitempty"`
	NextPassRun    string `json:"next_pass_run,omitempty"`
	DetectorAtPass string `json:"detector_at_pass"`
}

func followupOf(f checks.BlockFollowup) Followup {
	out := Followup{NextStop: runFact(f.NextStop), SessionTree: f.SessionTree, NextRun: runFact(f.NextRun)}
	for _, c := range f.Checks {
		fact := CheckFact{Check: c.Blocked.CheckID, Blocked: c.Blocked.Outcome, DetectorAtPass: c.Detector}
		if c.Next != nil {
			fact.Next = c.Next.Outcome
		}
		if c.NextPass != nil {
			fact.NextPassRun = c.NextPass.RunID
		}
		out.Checks = append(out.Checks, fact)
	}
	return out
}

func runFact(r *checks.OracleRun) *RunFact {
	if r == nil {
		return nil
	}
	return &RunFact{RunID: r.RunID, Source: r.Source, Session: r.Session,
		Started: r.Started.UTC().Format(time.RFC3339), Outcome: r.Outcome, Disposition: r.Disposition}
}

// ShowCheck assembles the block of run runID for reading. A transcript that
// cannot be read is reported in the reading rather than failing the call.
func ShowCheck(cases []CheckCase, runID string, receipts []adjudication.Receipt,
	budget transcript.ContinuationBudget) (CheckShow, error) {
	c, ok := FindCheckCase(cases, runID)
	if !ok {
		return CheckShow{}, fmt.Errorf("no recorded Stop block has the run %q", runID)
	}
	var prior []adjudication.Receipt
	for _, r := range receipts {
		if r.Subject == adjudication.SubjectCheck && r.RunID == runID {
			prior = append(prior, r)
		}
	}
	return CheckShow{Case: c, Followup: followupOf(c.Followup), Reading: c.Reading(budget), Prior: prior}, nil
}

// RenderCheck writes one check block at one clip level. FitCheck calls it repeatedly.
type RenderCheck func(CheckShow, Emission) ([]byte, error)

// FitCheck renders a check block within an allowance, withholding prior
// readings first, then continuation steps from the tail, then context steps
// from the end furthest from the block. It never withholds the run or its
// failures. A zero allowance withholds nothing and still reports the size.
func FitCheck(c CheckShow, max int, render RenderCheck) ([]byte, Emission, error) {
	steps, context := len(c.Reading.Steps), len(c.Reading.Context)
	base := Emission{MaxBytes: max, StepsAvailable: steps, ContextAvailable: context}

	type level struct {
		prior          bool
		steps, context int
	}
	levels := []level{{prior: true, steps: steps, context: context}}
	if len(c.Prior) > 0 {
		levels = append(levels, level{steps: steps, context: context})
	}
	for n := steps - 1; n >= 0; n-- {
		levels = append(levels, level{steps: n, context: context})
	}
	for n := context - 1; n >= 0; n-- {
		levels = append(levels, level{context: n})
	}

	var smallest int
	for _, l := range levels {
		trial := c
		if !l.prior {
			trial.Prior = nil
		}
		trial.Reading.Steps = c.Reading.Steps[:l.steps]
		trial.Reading.Context = c.Reading.Context[context-l.context:]

		e := base
		e.StepsEmitted, e.ContextEmitted = l.steps, l.context
		if !l.prior && len(c.Prior) > 0 {
			e.Clipped = append(e.Clipped,
				fmt.Sprintf("%d prior reading(s) withheld to fit the allowance", len(c.Prior)))
		}
		if l.steps < steps {
			e.Clipped = append(e.Clipped, fmt.Sprintf(
				"%d of %d continuation step(s) withheld to fit the allowance, from the end furthest from the block",
				steps-l.steps, steps))
		}
		if l.context < context {
			e.Clipped = append(e.Clipped, fmt.Sprintf(
				"%d of %d context step(s) withheld to fit the allowance, from the end furthest from the block",
				context-l.context, context))
		}
		size, out, err := settle(trial, e, render)
		if err != nil {
			return nil, Emission{}, err
		}
		if max <= 0 || size <= max {
			e.EmittedBytes = size
			return out, e, nil
		}
		smallest = size
	}
	return nil, Emission{}, fmt.Errorf(
		"the allowance of %d byte(s) does not carry this block's run and failures, "+
			"which need %d; raise --max-bytes or leave the case unread", max, smallest)
}

// RenderCheckJSON is the JSON rendering, valid at every clip level because a
// clip drops whole values rather than cutting inside one.
func RenderCheckJSON(c CheckShow, e Emission) ([]byte, error) {
	out, err := json.MarshalIndent(struct {
		CheckShow
		Emission Emission `json:"emission"`
	}{c, e}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
