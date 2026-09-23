package firingaudit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func TestReachReportsWhatTheRowsLocatorsAllow(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	if got := row.Reach(); got.Granularity != transcript.JoinCall {
		t.Errorf("a Claude Code row with both locators = %q (%s); want %q",
			got.Granularity, got.Reason, transcript.JoinCall)
	}

	gone := ccRow(t, "b", "2026-09-20T01:00:00Z")
	absent := filepath.Join(t.TempDir(), "absent.jsonl")
	gone.Locators["transcript_path"] = absent
	got := gone.Reach()
	if got.Granularity != transcript.JoinUnavailable || !strings.Contains(got.Reason, "gone") {
		t.Errorf("a missing transcript = %q (%s); want unavailable naming it",
			got.Granularity, got.Reason)
	}
	if got.TranscriptPath != absent || got.CallID != "toolu_b" {
		t.Errorf("reach = %+v; want the locators it looked at kept", got)
	}

	bare := ccRow(t, "c", "2026-09-20T01:00:00Z")
	bare.Locators = nil
	if got := bare.Reach(); got.Granularity != transcript.JoinUnavailable {
		t.Errorf("a row without locators = %q; want unavailable", got.Granularity)
	}

	// A Codex row reaches its turn. Its call identifier stays off the reach:
	// a blocked call has no rollout entry, so nothing would read it.
	codex := codexRow(t, "d", "2026-09-20T01:00:00Z")
	reach := codex.Reach()
	if reach.Granularity != transcript.JoinTurn {
		t.Errorf("a Codex row = %q (%s); want %q",
			reach.Granularity, reach.Reason, transcript.JoinTurn)
	}
	if reach.TurnID != "turn_d" || reach.CallID != "" {
		t.Errorf("reach = %+v; want the turn named and no call claimed", reach)
	}

	noTurn := codexRow(t, "e", "2026-09-20T01:00:00Z")
	delete(noTurn.Locators, "turn_id")
	if got := noTurn.Reach(); got.Granularity != transcript.JoinUnavailable ||
		!strings.Contains(got.Reason, "turn identifier") {
		t.Errorf("a Codex row without its turn = %q (%s); want unavailable naming it",
			got.Granularity, got.Reason)
	}

	// Pi has no lookup yet, and says so in its own terms rather than reading
	// as a row whose evidence is missing.
	pi := ccRow(t, "f", "2026-09-20T01:00:00Z")
	pi.Harness = "pi"
	if got := pi.Reach(); got.Granularity != transcript.JoinUnavailable ||
		!strings.Contains(got.Reason, "Pi") {
		t.Errorf("a Pi row = %q (%s); want unavailable naming Pi",
			got.Granularity, got.Reason)
	}
}

func TestContinuationDispatchesToTheHarnessThatRecordedTheRow(t *testing.T) {
	got, err := codexRow(t, "a", "2026-09-20T01:00:00Z").
		Continuation(transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the Codex reading failed: %v", err)
	}
	if got.Granularity != transcript.JoinTurn || len(got.Steps) != 2 {
		t.Fatalf("a Codex row read as %q with %d steps; want %q with 2",
			got.Granularity, len(got.Steps), transcript.JoinTurn)
	}
	if got.Harness != harness.Codex {
		t.Errorf("the reading named harness %q; want %q", got.Harness, harness.Codex)
	}

	cc, err := ccRow(t, "b", "2026-09-20T01:00:00Z").
		Continuation(transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the Claude Code reading failed: %v", err)
	}
	if cc.Granularity != transcript.JoinCall {
		t.Errorf("a Claude Code row read as %q; want %q",
			cc.Granularity, transcript.JoinCall)
	}
}

func TestRecordStampsTheTurnGranularityItReached(t *testing.T) {
	row := codexRow(t, "a", "2026-09-20T01:00:00Z")
	dir := t.TempDir()

	receipt, err := Record([]Row{row}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Prevented,
		Rationale:             "the command used bare python",
		ContinuationInspected: true,
		ContinuationOutcome:   "the task continued with python3",
	}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the judgment failed: %v", err)
	}
	// The turn is what was read, and the receipt says so rather than naming
	// the harness: a reader of the receipt needs the attribution limit, not
	// the harness's name.
	if receipt.Join != transcript.JoinTurn {
		t.Errorf("join = %q; want %q", receipt.Join, transcript.JoinTurn)
	}
	if receipt.JoinSteps != 2 {
		t.Errorf("join steps = %d; want the 2 read", receipt.JoinSteps)
	}
	if receipt.JoinAnchors == nil || receipt.JoinAnchors.TurnID != "turn_a" {
		t.Errorf("anchors = %+v; want the turn that was opened", receipt.JoinAnchors)
	}
	if receipt.JoinAnchors.DenialEntry != "" {
		t.Errorf("anchors = %+v; want no entry claimed for a call Codex never recorded",
			receipt.JoinAnchors)
	}
}

func TestRecordKeepsTheJudgmentWhenTheTranscriptWillNotOpen(t *testing.T) {
	// The path passes the existence test and then fails to read, which is a
	// limit on the continuation and not on the reading of the action.
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	row.Locators["transcript_path"] = t.TempDir()
	dir := t.TempDir()

	receipt, err := Record([]Row{row}, dir, Judgment{
		FiringID:  "a",
		Judgment:  adjudication.Incorrect,
		Rationale: "the command was ordinary work",
	}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("an unreadable transcript stopped the judgment: %v", err)
	}
	if receipt.Join != transcript.JoinUnavailable || receipt.JoinSteps != 0 {
		t.Errorf("join = %q at %d steps; want %q at 0",
			receipt.Join, receipt.JoinSteps, transcript.JoinUnavailable)
	}

	if _, err := Record([]Row{row}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Incorrect,
		ContinuationInspected: true,
	}, transcript.ContinuationBudget{}); err == nil {
		t.Error("a claim to have read the unreadable transcript was accepted")
	}

	c, err := Show([]Row{row}, "a", nil, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("an unreadable transcript stopped the case: %v", err)
	}
	if c.Continuation.Granularity != transcript.JoinUnavailable {
		t.Errorf("shown join = %q; want %q",
			c.Continuation.Granularity, transcript.JoinUnavailable)
	}
}

func TestRecordStampsTheImplementationAndTheJoinItself(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	row.MasterBuildDigest = "build1"
	dir := t.TempDir()

	receipt, err := Record([]Row{row}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Prevented,
		Rationale:             "the script would have raised",
		ContinuationInspected: true,
		ContinuationOutcome:   "the task carried on with python3",
		Selection:             "oldest unread",
	}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}

	if receipt.HookDigest != "hook1" || receipt.RuleHash != "rule1" ||
		receipt.MasterBuildDigest != "build1" {
		t.Errorf("receipt stamps = %+v; want the row's own", receipt)
	}
	if receipt.HookID != "guard" || receipt.Harness != "claude-code" {
		t.Errorf("receipt identity = %+v; want the row's hook and harness", receipt)
	}
	if receipt.Join != transcript.JoinCall || receipt.JoinSteps != 2 {
		t.Errorf("join = %q at %d steps; want %q at 2",
			receipt.Join, receipt.JoinSteps, transcript.JoinCall)
	}
	if receipt.JoinAnchors == nil || receipt.JoinAnchors.DenialEntry != "denial" {
		t.Errorf("anchors = %+v; want the denial entry the walk read", receipt.JoinAnchors)
	}
	if receipt.JudgedAt == "" {
		t.Error("the receipt carries no time")
	}

	stored, err := adjudication.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].FiringID != "a" {
		t.Errorf("stored = %+v; want the one receipt", stored)
	}
}

func TestRecordRefusesAJudgmentItCannotStandBehind(t *testing.T) {
	gone := ccRow(t, "a", "2026-09-20T01:00:00Z")
	gone.Locators["transcript_path"] = filepath.Join(t.TempDir(), "absent.jsonl")
	dir := t.TempDir()

	_, err := Record([]Row{gone}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Prevented,
		ContinuationInspected: true,
	}, transcript.ContinuationBudget{})
	if err == nil {
		t.Error("a claim to have read an unreachable continuation was accepted")
	}

	// The same firing judged without that claim is ordinary work.
	receipt, err := Record([]Row{gone}, dir, Judgment{
		FiringID: "a",
		Judgment: adjudication.Prevented,
	}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Join != transcript.JoinUnavailable {
		t.Errorf("join = %q; want %q", receipt.Join, transcript.JoinUnavailable)
	}

	if _, err := Record([]Row{gone}, dir, Judgment{
		FiringID: "nothing",
		Judgment: adjudication.Prevented,
	}, transcript.ContinuationBudget{}); err == nil {
		t.Error("a judgment of a firing no row carries was accepted")
	}
}

func TestShowGathersTheActionTheContinuationAndThePriorReadings(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	row.Evidence = []byte(`{"tool_input":{"command":"python x.py"}}`)
	receipts := []adjudication.Receipt{
		{Subject: adjudication.SubjectGuard, FiringID: "other", Judgment: adjudication.Prevented},
		{Subject: adjudication.SubjectGuard, FiringID: "a", Judgment: adjudication.Unresolved},
	}

	c, err := Show([]Row{row}, "a", receipts, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Evidence.Source != Inline {
		t.Errorf("evidence = %q; want the inline bundle", c.Evidence.Source)
	}
	if c.Continuation.Granularity != transcript.JoinCall || len(c.Continuation.Steps) != 2 {
		t.Errorf("continuation = %q at %d steps; want %q at 2",
			c.Continuation.Granularity, len(c.Continuation.Steps), transcript.JoinCall)
	}
	if len(c.Prior) != 1 || c.Prior[0].Judgment != adjudication.Unresolved {
		t.Errorf("prior = %+v; want only this firing's judgment", c.Prior)
	}

	// The blocked action is what a reader judges, so it must survive the
	// serialization a caller hands to the reader.
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"command":"python x.py"`) {
		t.Errorf("encoded case dropped the blocked action: %s", encoded)
	}

	if _, err := Show([]Row{row}, "nothing", nil,
		transcript.ContinuationBudget{}); err == nil {
		t.Error("an identifier no row carries returned a case")
	}
}

func TestRecordKeepsWhatWasReadApartFromWhatTheLookupReached(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	dir := t.TempDir()

	// The reading saw one of the two steps, because a byte allowance withheld
	// the other. The receipt says both numbers.
	receipt, err := Record([]Row{row}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Prevented,
		ContinuationInspected: true,
		InspectedSteps:        1,
	}, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.JoinSteps != 2 || receipt.InspectedSteps != 1 {
		t.Errorf("join_steps=%d inspected_steps=%d; want 2 reached and 1 read",
			receipt.JoinSteps, receipt.InspectedSteps)
	}

	// An extent above what the lookup reached is a claim about evidence that
	// was never there.
	if _, err := Record([]Row{row}, dir, Judgment{
		FiringID:              "a",
		Judgment:              adjudication.Prevented,
		ContinuationInspected: true,
		InspectedSteps:        3,
	}, transcript.ContinuationBudget{}); err == nil {
		t.Error("an extent beyond the lookup's reach was accepted")
	}

	// An extent without the inspection it describes is the same false record.
	if _, err := Record([]Row{row}, dir, Judgment{
		FiringID:       "a",
		Judgment:       adjudication.Prevented,
		InspectedSteps: 1,
	}, transcript.ContinuationBudget{}); err == nil {
		t.Error("read steps were accepted beside an uninspected continuation")
	}
}
