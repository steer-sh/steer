package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/firingaudit"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func checkBlock(id string) firingaudit.CheckCase {
	run := checks.OracleRun{RunID: id, Repo: "/r", Source: checks.SourceStop, Harness: "codex",
		Session: "s1", Outcome: checks.OutcomeFailed, Disposition: checks.DispositionBlock,
		Started: time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC), Excerpt: "ok a\n--- FAIL: TestX\npanic: boom"}
	return firingaudit.CheckCase{Run: run, Failures: firingaudit.CheckFailures(run, nil)}
}

// A queue holding a check block names its repository, its reason and each
// failure's applicability, and a store with no guard audit is not "nothing".
func TestWriteQueueListsACheckBlock(t *testing.T) {
	items := []firingaudit.Item{{Subject: adjudication.SubjectCheck, RunID: "1a", Repo: "/r",
		Timestamp: "2026-09-23T03:00:00Z", Reason: firingaudit.Unjudged, Reach: transcript.JoinEntry,
		Failures: []firingaudit.ItemFailure{{Kind: adjudication.FailureCheck, Check: "go", Required: true, Applies: adjudication.Historic}}}}
	var out bytes.Buffer
	err := writeQueue(&out, false, items, firingaudit.Counts{Queued: 1, Listed: 1, Unjudged: 1}, nil,
		[]firingaudit.CheckCoverage{{Repo: "/r", Blocks: 3, Judged: 2, Unjudged: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check.sh in /r: 3 Stop blocks, 2 of those read", "1a",
		"check.sh block in /r", "reach=" + transcript.JoinEntry, "check:go(applies=historic)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("queue output missing %q:\n%s", want, out.String())
		}
	}
}

// The text view carries the failures, what followed, both sides of the block
// and the recorded excerpt whole, ending on its own account.
func TestRenderCheckCaseShowsTheBlockAndBothSidesOfIt(t *testing.T) {
	c := checkBlock("1a")
	c.Failures[0].Applies = adjudication.Unmanaged
	show := firingaudit.CheckShow{Case: c,
		Followup: firingaudit.Followup{SessionTree: checks.FollowChanged,
			NextStop: &firingaudit.RunFact{RunID: "2b", Disposition: checks.DispositionAllow, Started: "2026-09-23T03:05:00Z"}},
		Reading: transcript.StopBlockReading{
			Continuation: transcript.Continuation{Granularity: transcript.JoinEntry, EndReason: transcript.EndInterrupted,
				Steps: []transcript.ContinuationStep{{Kind: transcript.StepHookFeedback, Text: "check.sh failed"}}},
			Context: []transcript.ContinuationStep{{Kind: transcript.StepHumanTurn, Text: "add the feature"}},
			Matches: 2, ContextOmitted: 4},
		Prior: []adjudication.Receipt{{Subject: adjudication.SubjectCheck, RunID: "1a", Rationale: "flaky",
			Failures: []adjudication.Failure{{Kind: adjudication.FailureOracle, Validity: adjudication.Invalid, Attribution: adjudication.PreExisting}}}},
	}
	out, err := renderCheckCase(show, firingaudit.Emission{ContextEmitted: 1, ContextAvailable: 1, StepsEmitted: 1, StepsAvailable: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check.sh run 1a blocked a codex turn", "oracle", "required", "applies=unmanaged",
		"    panic: boom", "next Stop in this session: allow", "tree changed", "join=" + transcript.JoinEntry,
		"2 entries carry this block's reason", "4 step(s) between the prompt and the block",
		"before human_turn", "add the feature", "after  hook_feedback", "ended where the turn was interrupted",
		"oracle=invalid,pre-existing", "flaky", "1 context step(s) of 1 and 1 continuation step(s) of 1"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// An identifier both a firing and a run carry needs --subject; either alone
// resolves to its own subject.
func TestResolveSubjectRefusesAnAmbiguousIdentifier(t *testing.T) {
	source := auditSource{
		Rows:  []firingaudit.Row{{FiringID: "same"}, {FiringID: "guardonly"}},
		Cases: []firingaudit.CheckCase{checkBlock("same"), checkBlock("runonly")},
	}
	if _, err := resolveSubject(source, "same", ""); err == nil {
		t.Error("an identifier naming both was resolved")
	}
	for id, want := range map[string]string{"guardonly": adjudication.SubjectGuard, "runonly": adjudication.SubjectCheck} {
		if got, err := resolveSubject(source, id, ""); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", id, got, err, want)
		}
	}
	if got, err := resolveSubject(source, "same", adjudication.SubjectCheck); err != nil || got != adjudication.SubjectCheck {
		t.Errorf("an explicit subject = %q, %v; want it kept", got, err)
	}
	if _, err := resolveSubject(source, "same", "review"); err == nil {
		t.Error("an unknown subject was accepted")
	}
}

// Rows lists check blocks beside guard rows in one order; a subject, a guard
// name or the excluded filter narrows it.
func TestListRowsInterleavesBlocksAndRows(t *testing.T) {
	source := auditSource{
		Rows: []firingaudit.Row{
			{FiringID: "f1", HookName: "g", RowDigest: "d1", Timestamp: "2026-09-23T02:00:00Z"},
			{FiringID: "f2", HookName: "g", RowDigest: "d2", Timestamp: "2026-09-23T04:00:00+00:00"},
		},
		Cases: []firingaudit.CheckCase{checkBlock("1a")},
	}
	names := func(lines []rowLine) string {
		var out []string
		for _, l := range lines {
			out = append(out, l.FiringID+l.RunID)
		}
		return strings.Join(out, ",")
	}
	if got := names(listRows(source, "", "", false)); got != "f1,1a,f2" {
		t.Errorf("rows = %s; want f1, the block, f2 by time", got)
	}
	if got := names(listRows(source, adjudication.SubjectCheck, "", false)); got != "1a" {
		t.Errorf("check rows = %s", got)
	}
	if got := names(listRows(source, "", "g", false)); got != "f1,f2" {
		t.Errorf("one guard's rows = %s", got)
	}
}
