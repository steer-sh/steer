package firingaudit

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// mixedCase is a block with a failing check whose record carries a detector, and
// check.sh's own optional failure.
func mixedCase(t *testing.T) CheckCase {
	t.Helper()
	c := readableCase(t, "codex", "turn_id")
	e := record("/r", "1a", "go", checks.OutcomeFailed)
	e.DetectorDigest = "d1"
	c.Executions = []checks.Execution{e}
	c.Failures = CheckFailures(c.Run, c.Executions)
	return c
}

// Guard firings and check blocks share one queue in one order; a subject or a
// guard name narrows it, and a judged block whose reachable continuation nobody
// read returns as a guard firing does.
func TestQueueOrdersFiringsAndBlocksTogether(t *testing.T) {
	early := ccRow(t, "f1", "2026-09-23T02:00:00Z")
	late := ccRow(t, "f2", "2026-09-23T04:00:00+00:00")
	block := mixedCase(t) // started 2026-09-23T03:01:00Z
	cases := []CheckCase{block}

	items, counts := Queue([]Row{late, early}, cases, nil, QueueOptions{Installed: installed})
	if got := ids(items); strings.Join(got, ",") != "f1,1a,f2" {
		t.Fatalf("queue = %v; want f1, the block, f2 by time", got)
	}
	if b := items[1]; b.Subject != adjudication.SubjectCheck || b.Repo != "/r" || len(b.Failures) != 2 ||
		!b.Failures[0].Required || b.Failures[1].Kind != adjudication.FailureOracle {
		t.Errorf("block item = %+v; want the check subject with its two failures", b)
	}
	if counts.Queued != 3 || counts.Unjudged != 3 {
		t.Errorf("counts = %+v; want three unread", counts)
	}

	if got, _ := Queue([]Row{early}, cases, nil, QueueOptions{Installed: installed, Subject: adjudication.SubjectCheck}); strings.Join(ids(got), ",") != "1a" {
		t.Errorf("check subject = %v; want only the block", ids(got))
	}
	if got, _ := Queue([]Row{early}, cases, nil, QueueOptions{Installed: installed, Subject: adjudication.SubjectGuard}); strings.Join(ids(got), ",") != "f1" {
		t.Errorf("guard subject = %v; want only the firing", ids(got))
	}
	if got, _ := Queue([]Row{early}, cases, nil, QueueOptions{Installed: installed, HookName: "guard"}); strings.Join(ids(got), ",") != "f1" {
		t.Errorf("one guard = %v; want no block", ids(got))
	}

	read := []adjudication.Receipt{{Subject: adjudication.SubjectCheck, RunID: "1a",
		Failures: []adjudication.Failure{{Kind: adjudication.FailureCheck, Check: "go",
			Validity: adjudication.Valid, Attribution: adjudication.Introduced}}}}
	got, _ := Queue(nil, cases, read, QueueOptions{})
	if len(got) != 1 || got[0].Reason != ContinuationUnread {
		t.Errorf("judged block = %+v; want it back for its unread continuation", got)
	}
	read[0].ContinuationInspected = true
	if got, _ := Queue(nil, cases, read, QueueOptions{}); len(got) != 0 {
		t.Errorf("fully read block = %+v; want none", got)
	}
}

// A judgment names only failures the run reported and reads every required one;
// the record's stamps are copied, not supplied.
func TestRecordCheckReadsEachReportedFailure(t *testing.T) {
	c := mixedCase(t)
	dir := t.TempDir()
	valid := adjudication.Failure{Kind: adjudication.FailureCheck, Check: "go",
		Validity: adjudication.Valid, Attribution: adjudication.Introduced}
	for name, failures := range map[string][]adjudication.Failure{
		"no required failure": {{Kind: adjudication.FailureOracle, Validity: adjudication.Valid, Attribution: adjudication.Introduced}},
		"an unreported check": {valid, {Kind: adjudication.FailureCheck, Check: "lint", Validity: adjudication.Valid, Attribution: adjudication.Introduced}},
		"an unreported kind":  {valid, {Kind: adjudication.FailureIntegration, Validity: adjudication.Valid, Attribution: adjudication.Introduced}},
		"a failure twice":     {valid, valid},
	} {
		if _, err := RecordCheck([]CheckCase{c}, dir, CheckJudgment{RunID: "1a", Failures: failures}, transcript.ContinuationBudget{}); err == nil {
			t.Errorf("%s: the judgment was accepted", name)
		}
	}
	if _, err := RecordCheck([]CheckCase{c}, dir, CheckJudgment{RunID: "zz", Failures: []adjudication.Failure{valid}}, transcript.ContinuationBudget{}); err == nil {
		t.Error("a judgment of a run no case holds was accepted")
	}
	if _, err := RecordCheck([]CheckCase{c}, dir, CheckJudgment{RunID: "1a", Failures: []adjudication.Failure{valid},
		InspectedSteps: 1}, transcript.ContinuationBudget{}); err == nil {
		t.Error("inspected steps without an inspected continuation were accepted")
	}

	receipt, err := RecordCheck([]CheckCase{c}, dir, CheckJudgment{RunID: "1a",
		Failures:              []adjudication.Failure{valid},
		ContinuationInspected: true, InspectedSteps: 1}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	f := receipt.Failures[0]
	if receipt.Subject != adjudication.SubjectCheck || receipt.Repo != "/r" || receipt.Join != transcript.JoinEntry ||
		receipt.JoinSteps != 1 || f.DetectorDigest != "d1" || f.RecordedOutcome != checks.OutcomeFailed {
		t.Errorf("receipt = %+v; want the run's stamps and the entry join", receipt)
	}
	stored, _ := adjudication.Load(dir)
	if len(stored) != 1 || stored[0].RunID != "1a" {
		t.Errorf("stored = %+v; want the one receipt", stored)
	}
}

// Show carries the case, what followed and only this run's prior check readings.
func TestShowCheckAssemblesTheBlockForReading(t *testing.T) {
	c := mixedCase(t)
	next := blockRun("/r", "2b", 5)
	c.Followup = checks.BlockFollowup{Block: c.Run, NextRun: &next, SessionTree: checks.FollowUnknown}
	receipts := []adjudication.Receipt{
		{Subject: adjudication.SubjectGuard, FiringID: "1a", Judgment: adjudication.Prevented},
		{Subject: adjudication.SubjectCheck, RunID: "1a"},
	}
	got, err := ShowCheck([]CheckCase{c}, "1a", receipts, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Prior) != 1 || got.Prior[0].Subject != adjudication.SubjectCheck {
		t.Errorf("prior = %+v; want only the check reading", got.Prior)
	}
	if got.Followup.NextRun == nil || got.Followup.NextRun.RunID != "2b" || got.Reading.Granularity != transcript.JoinEntry {
		t.Errorf("show = %+v; want the next run and the entry reading", got)
	}
	if _, err := ShowCheck(nil, "1a", nil, transcript.ContinuationBudget{}); err == nil {
		t.Error("a run no case holds was shown")
	}
}

// Under an allowance, prior readings go first, then continuation steps from the
// tail, then context from the end furthest from the block; the run never goes.
func TestFitCheckWithholdsTheLeastDecisiveEvidenceFirst(t *testing.T) {
	step := func(text string) transcript.ContinuationStep {
		return transcript.ContinuationStep{Kind: transcript.StepAssistantText, Text: strings.Repeat(text, 200)}
	}
	show := CheckShow{Case: mixedCase(t), Prior: []adjudication.Receipt{{Subject: adjudication.SubjectCheck, RunID: "1a", Rationale: strings.Repeat("p", 400)}}}
	show.Reading.Context = []transcript.ContinuationStep{step("o"), step("n")}
	show.Reading.Steps = []transcript.ContinuationStep{step("a"), step("z")}

	full, e, err := FitCheck(show, 0, RenderCheckJSON)
	if err != nil || e.EmittedBytes != len(full) || e.StepsEmitted != 2 || e.ContextEmitted != 2 {
		t.Fatalf("unbounded = %d bytes, %+v, %v; want everything and its exact size", len(full), e, err)
	}
	// Walk the allowance down; each withholding must come in order.
	lastSteps, lastContext, sawContextKept := 2, 2, false
	for max := len(full); max > 0; max -= 50 {
		out, e, err := FitCheck(show, max, RenderCheckJSON)
		if err != nil {
			break
		}
		if e.EmittedBytes > max || e.EmittedBytes != len(out) {
			t.Fatalf("at %d: emitted %d, output %d; want the exact size within the allowance", max, e.EmittedBytes, len(out))
		}
		withheldPrior := len(e.Clipped) > 0 && strings.Contains(e.Clipped[0], "prior")
		if (e.StepsEmitted < 2 || e.ContextEmitted < 2) && !withheldPrior {
			t.Errorf("at %d: steps withheld while the prior reading stayed", max)
		}
		if e.ContextEmitted < 2 && e.StepsEmitted > 0 {
			t.Errorf("at %d: context withheld while %d continuation step(s) stayed", max, e.StepsEmitted)
		}
		if e.StepsEmitted > lastSteps || e.ContextEmitted > lastContext {
			t.Errorf("at %d: a smaller allowance kept more", max)
		}
		if e.ContextEmitted == 1 {
			sawContextKept = strings.Contains(string(out), strings.Repeat("n", 200)) &&
				!strings.Contains(string(out), strings.Repeat("o", 200))
		}
		lastSteps, lastContext = e.StepsEmitted, e.ContextEmitted
	}
	if !sawContextKept {
		t.Error("no allowance kept only the context step nearest the block")
	}
	if _, _, err := FitCheck(show, 100, RenderCheckJSON); err == nil {
		t.Error("an allowance that cannot carry the run was accepted")
	}
}

func TestParseFailureReadsEachNameForm(t *testing.T) {
	for spec, want := range map[string]adjudication.Failure{
		"check:go-test=valid,introduced":       {Kind: "check", Check: "go-test", Validity: "valid", Attribution: "introduced"},
		"oracle=invalid,pre-existing,declined": {Kind: "oracle", Validity: "invalid", Attribution: "pre-existing", Continuation: "declined"},
		"integration=valid,unresolved":         {Kind: "integration", Validity: "valid", Attribution: "unresolved"},
	} {
		got, err := ParseFailure(spec)
		if err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"go-test=valid,introduced", "check:=valid,introduced", "oracle:x=valid,introduced",
		"oracle=valid", "oracle=valid,introduced,fixed,extra", "oracle"} {
		if _, err := ParseFailure(spec); err == nil {
			t.Errorf("%s was accepted", spec)
		}
	}
}
