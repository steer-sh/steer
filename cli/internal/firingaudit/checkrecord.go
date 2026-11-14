package firingaudit

import (
	"fmt"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// CheckJudgment is what a reader concluded about one check block. Each Failure
// names its kind and check and carries the reader's three readings; everything
// else on the receipt is resolved from the run's records.
type CheckJudgment struct {
	RunID                 string
	Failures              []adjudication.Failure
	Rationale             string
	Evidence              []string
	ContinuationInspected bool
	ContinuationOutcome   string
	InspectedSteps        int
	Selection             string
}

// RecordCheck writes one judgment of a check block to the adjudication stream in
// dir, and returns the receipt written.
//
// The failures judged must be ones the run reported, and every required one
// must be judged. Record copies each failure's recorded outcome and detector
// digest from its execution and resolves the reading to stamp the join.
func RecordCheck(cases []CheckCase, dir string, j CheckJudgment,
	budget transcript.ContinuationBudget) (adjudication.Receipt, error) {
	c, ok := FindCheckCase(cases, j.RunID)
	if !ok {
		return adjudication.Receipt{}, fmt.Errorf("no recorded Stop block has the run %q", j.RunID)
	}
	failures, err := stampFailures(c, j.Failures)
	if err != nil {
		return adjudication.Receipt{}, err
	}
	reading := c.Reading(budget)
	if j.ContinuationInspected && !reading.Reached() {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports an inspected continuation and none was reached: %s", reading.Reason)
	}
	if j.InspectedSteps > 0 && !j.ContinuationInspected {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports %d inspected continuation step(s) and no inspected continuation", j.InspectedSteps)
	}
	if j.InspectedSteps > len(reading.Steps) {
		return adjudication.Receipt{}, fmt.Errorf(
			"the judgment reports %d inspected continuation step(s) and the lookup reached %d",
			j.InspectedSteps, len(reading.Steps))
	}
	anchors := reading.Anchors
	receipt := adjudication.Receipt{
		Subject:   adjudication.SubjectCheck,
		RunID:     c.Run.RunID,
		Repo:      c.Run.Repo,
		Harness:   c.Run.Harness,
		Failures:  failures,
		Rationale: j.Rationale,
		Evidence:  j.Evidence,

		ContinuationInspected: j.ContinuationInspected,
		ContinuationOutcome:   j.ContinuationOutcome,
		Join:                  reading.Granularity,
		JoinAnchors:           &anchors,
		JoinSteps:             len(reading.Steps),
		InspectedSteps:        j.InspectedSteps,

		Selection: j.Selection,
		JudgedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := adjudication.Append(dir, receipt); err != nil {
		return adjudication.Receipt{}, err
	}
	return receipt, nil
}

// stampFailures matches each judged failure to one the run reported and copies
// its record's stamps, in the order the case lists them.
func stampFailures(c CheckCase, judged []adjudication.Failure) ([]adjudication.Failure, error) {
	byName := map[string]adjudication.Failure{}
	for _, f := range judged {
		name := FailureName(f.Kind, f.Check)
		if _, dup := byName[name]; dup {
			return nil, fmt.Errorf("the judgment reads failure %s twice", name)
		}
		byName[name] = f
	}
	var out []adjudication.Failure
	var missing []string
	for _, reported := range c.Failures {
		name := FailureName(reported.Kind, reported.Check)
		f, ok := byName[name]
		if !ok {
			if reported.Required {
				missing = append(missing, name)
			}
			continue
		}
		delete(byName, name)
		if reported.Execution != nil {
			f.RecordedOutcome = reported.Execution.Outcome
			f.DetectorDigest = reported.Execution.DetectorDigest
		}
		out = append(out, f)
	}
	if len(byName) > 0 {
		var extra []string
		for name := range byName {
			extra = append(extra, name)
		}
		return nil, fmt.Errorf("run %s reported no failure %s; it reported %s",
			c.Run.RunID, strings.Join(extra, ", "), reportedNames(c))
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the judgment must read every failure the run requires: %s is unread",
			strings.Join(missing, ", "))
	}
	return out, nil
}

func reportedNames(c CheckCase) string {
	var names []string
	for _, f := range c.Failures {
		names = append(names, FailureName(f.Kind, f.Check))
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// FailureName is how the CLI names a failure: check:ID for a declared check,
// oracle or integration otherwise.
func FailureName(kind, check string) string {
	if kind == adjudication.FailureCheck {
		return kind + ":" + check
	}
	return kind
}

// ParseFailure reads one --failure value: NAME=VALIDITY,ATTRIBUTION[,CONTINUATION],
// where NAME is check:ID, oracle or integration. Values are checked when the
// receipt is appended.
func ParseFailure(spec string) (adjudication.Failure, error) {
	name, readings, ok := strings.Cut(spec, "=")
	if !ok {
		return adjudication.Failure{}, fmt.Errorf(
			"invalid failure %q: expected NAME=VALIDITY,ATTRIBUTION[,CONTINUATION]", spec)
	}
	var f adjudication.Failure
	switch kind, check, _ := strings.Cut(name, ":"); kind {
	case adjudication.FailureCheck:
		if check == "" {
			return f, fmt.Errorf("invalid failure %q: check: needs the check's ID", spec)
		}
		f.Kind, f.Check = kind, check
	case adjudication.FailureOracle, adjudication.FailureIntegration:
		if name != kind {
			return f, fmt.Errorf("invalid failure %q: %s names no check", spec, kind)
		}
		f.Kind = kind
	default:
		return f, fmt.Errorf("invalid failure %q: NAME is check:ID, %s or %s",
			spec, adjudication.FailureOracle, adjudication.FailureIntegration)
	}
	parts := strings.Split(readings, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return f, fmt.Errorf("invalid failure %q: expected VALIDITY,ATTRIBUTION[,CONTINUATION]", spec)
	}
	f.Validity, f.Attribution = parts[0], parts[1]
	if len(parts) == 3 {
		f.Continuation = parts[2]
	}
	return f, nil
}
