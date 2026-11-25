package checks_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/firingaudit"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func transcriptLine(t *testing.T, fields map[string]any) string {
	t.Helper()
	line, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// One real Stop block goes through the whole loop on the live state: the
// queue offers it with its failures, show reads the transcript entry its reason
// landed in, record reads each failure, a contest and an exclusion name it by
// run, and the guard reader never sees any of it.
func TestAStopBlockGoesThroughTheAuditLoop(t *testing.T) {
	root, req := checks.StopRepoForTest(t, map[string]string{"a": "echo 'assertion failed' >&2\nexit 1\n"}, "")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	payload := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":%q,"transcript_path":%q,"prompt_id":"p1","stop_hook_active":false}`,
		req.Session, req.CWD, path)
	d, reply := checks.StopHook("claude-code", []byte(payload), checks.StopHookOptions{Timeout: req.Timeout, SteerBin: req.SteerBin})
	if d.Disposition != checks.DispositionBlock || reply == nil {
		t.Fatalf("stop = %+v; want a block", d)
	}
	lines := []string{
		transcriptLine(t, map[string]any{"uuid": "prompt", "parentUuid": nil, "type": "user", "promptId": "p1",
			"message": map[string]any{"role": "user", "content": "make the check pass"}}),
		transcriptLine(t, map[string]any{"uuid": "fb", "parentUuid": "prompt", "type": "user", "isMeta": true, "promptId": "p1",
			"message": map[string]any{"role": "user", "content": "Stop hook feedback:\n" + reply.Reason}}),
		transcriptLine(t, map[string]any{"uuid": "say", "parentUuid": "fb", "type": "assistant",
			"message": map[string]any{"content": []map[string]any{{"type": "text", "text": "the assertion is mine; fixing it"}}}}),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases, err := firingaudit.LoadCheckCases()
	if err != nil || len(cases) != 1 {
		t.Fatalf("cases = %d, %v; want the one block", len(cases), err)
	}
	firingaudit.ClassifyCases(cases, firingaudit.InstalledDetectors())
	runID := d.Run.RunID
	items, _ := firingaudit.Queue(nil, cases, nil, firingaudit.QueueOptions{})
	if len(items) != 1 || items[0].RunID != runID || items[0].Reach != transcript.JoinEntry || items[0].Repo != root {
		t.Fatalf("queue = %+v; want run %s reachable at its entry", items, runID)
	}
	if f := items[0].Failures; len(f) != 2 || f[0].Check != "a" || !f[0].Required ||
		f[0].Applies != adjudication.Current || f[1].Kind != adjudication.FailureOracle || f[1].Required {
		t.Errorf("failures = %+v; want check a required and current, then check.sh optional", f)
	}

	show, err := firingaudit.ShowCheck(cases, runID, nil, transcript.ContinuationBudget{})
	if err != nil || show.Reading.Anchors.DenialEntry != "fb" || len(show.Reading.Context) != 1 ||
		len(show.Reading.Steps) != 2 || show.Case.Run.ContestProtocol != checks.ContestProtocol {
		t.Fatalf("show = %+v, %v; want the feedback entry with the prompt before it and the reply after", show.Reading, err)
	}

	dir, err := statestore.Prepare(adjudication.Component)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := firingaudit.RecordCheck(cases, dir, firingaudit.CheckJudgment{RunID: runID,
		Failures: []adjudication.Failure{{Kind: adjudication.FailureCheck, Check: "a",
			Validity: adjudication.Valid, Attribution: adjudication.Introduced, Continuation: adjudication.Fixed}},
		ContinuationInspected: true, InspectedSteps: 2}, transcript.ContinuationBudget{})
	if err != nil || receipt.Failures[0].DetectorDigest == "" || receipt.Join != transcript.JoinEntry {
		t.Fatalf("receipt = %+v, %v; want the detector and the entry join stamped", receipt, err)
	}
	receipts, _ := adjudication.Load(dir)
	if items, _ := firingaudit.Queue(nil, cases, receipts, firingaudit.QueueOptions{}); len(items) != 0 {
		t.Errorf("queue after the reading = %+v; want it empty", items)
	}
	twin := firingaudit.Row{FiringID: runID, HookName: "g", HookDigest: "h", RuleHash: "r", Timestamp: "2026-09-23T00:00:00Z"}
	installed := func(firingaudit.Row) adjudication.Identity {
		return adjudication.Identity{HookDigest: "h", RuleHash: "r"}
	}
	guards, _ := firingaudit.Queue([]firingaudit.Row{twin}, cases, receipts,
		firingaudit.QueueOptions{Subject: adjudication.SubjectGuard, Installed: installed})
	if len(guards) != 1 || guards[0].Reason != firingaudit.Unjudged {
		t.Errorf("guard queue = %+v; want a firing named like the run still unread", guards)
	}

	if _, err := firingaudit.ContestCheck(cases, dir, runID, "the assertion tests another team's code", root); err != nil {
		t.Fatal(err)
	}
	contests, _ := adjudication.LoadContests(dir)
	report := firingaudit.Contests(nil, cases, contests, receipts, nil, nil, nil, "")
	if r := report.Checks; len(r) != 1 || r[0].Contested != 1 || r[0].NotOffered != 0 || r[0].Unread != 0 {
		t.Errorf("contests = %+v; want one offered, contested and read block", r)
	}

	if _, err := firingaudit.CorrectCheck(cases, dir, firingaudit.Correction{Ref: runID, Action: adjudication.Exclude,
		Reason: "demo session", Evidence: []string{path}}, nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := adjudication.LoadExclusions(dir)
	if cover := firingaudit.CoverChecks(cases, receipts, adjudication.ExcludedRuns(entries)); cover[0].Excluded != 1 {
		t.Errorf("coverage = %+v; want the block excluded", cover)
	}
	if runs, _ := checks.OracleRuns(root); len(runs) != 1 {
		t.Errorf("runs = %d; want the excluded block's record kept", len(runs))
	}
}
