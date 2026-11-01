package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// blockedTurn reproduces the shape of a rollout that held a blocked call: one
// turn of context before the turn in question, then the turn itself, whose
// exec call carries the block in its own result. Nothing in it records the
// blocked call, because a blocked call never runs.
const blockedTurn = `{"timestamp":"2026-09-20T16:42:33.671Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/repo"}}
{"timestamp":"2026-09-20T16:42:34.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-0"}}
{"timestamp":"2026-09-20T16:42:34.100Z","ordinal":2,"type":"response_item","payload":{"type":"message","id":"m0","role":"user","content":[{"type":"input_text","text":"an earlier question"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-0","content_item_kinds":["user.text"]}}}
{"timestamp":"2026-09-20T16:42:34.200Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-0"}}
{"timestamp":"2026-09-20T16:42:35.000Z","ordinal":4,"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-09-20T16:42:35.100Z","ordinal":5,"type":"response_item","payload":{"type":"message","id":"m1","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>a long block of harness text</skills_instructions>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:35.200Z","ordinal":6,"type":"response_item","payload":{"type":"message","id":"m2","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>none</recommended_plugins>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","content_item_kinds":["plugins.recommended"]}}}
{"timestamp":"2026-09-20T16:42:36.416Z","ordinal":7,"type":"response_item","payload":{"type":"message","id":"m3","role":"user","content":[{"type":"input_text","text":"run python -c 'print(1)' and report the result"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","content_item_kinds":["user.text"]}}}
{"timestamp":"2026-09-20T16:42:37.000Z","ordinal":8,"type":"response_item","payload":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"gAAAA","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:37.500Z","ordinal":9,"type":"response_item","payload":{"type":"reasoning","id":"rs_2","summary":[{"type":"summary_text","text":"**Running the command as asked**"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:38.884Z","ordinal":10,"type":"response_item","payload":{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/helper","content":[{"type":"input_text","text":"Message Type: NEW_TASK"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:39.000Z","ordinal":11,"type":"response_item","payload":{"type":"message","id":"m4","role":"assistant","content":[{"type":"output_text","text":"I will run it now."}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:41.242Z","ordinal":12,"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call_a","input":"for (const cmd of cmds) { await tools.exec_command({cmd}); }\n","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:41.455Z","ordinal":13,"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_a","output":[{"type":"input_text","text":"Script failed"},{"type":"input_text","text":"Command blocked by PreToolUse hook: use python3 instead."}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:42.000Z","ordinal":14,"type":"token_usage_record","payload":{"turn_id":"turn-1","usage":{}}}
{"timestamp":"2026-09-20T16:42:45.183Z","ordinal":15,"type":"response_item","payload":{"type":"message","id":"m5","role":"assistant","content":[{"type":"output_text","text":"The command was blocked by the hook. I did not retry it."}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}
{"timestamp":"2026-09-20T16:42:45.500Z","ordinal":16,"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
{"timestamp":"2026-09-20T16:42:50.000Z","ordinal":17,"type":"response_item","payload":{"type":"message","id":"m6","role":"user","content":[{"type":"input_text","text":"a later question"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-2","content_item_kinds":["user.text"]}}}
`

func writeTurns(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func kinds(c transcript.Continuation) []transcript.StepKind {
	var out []transcript.StepKind
	for _, s := range c.Steps {
		out = append(out, s.Kind)
	}
	return out
}

func TestContinuationReadsTheTurnWholeAndNoOtherTurn(t *testing.T) {
	got, err := Continuation(writeTurns(t, blockedTurn), "turn-1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if got.Granularity != transcript.JoinTurn {
		t.Fatalf("granularity = %q (%s); want %q",
			got.Granularity, got.Reason, transcript.JoinTurn)
	}
	// The prompt opens the turn, the harness's own messages read as nothing,
	// and the turn's last word is the assistant's, not the next turn's prompt.
	want := []transcript.StepKind{
		transcript.StepHumanTurn,
		transcript.StepThinking,
		transcript.StepAssistantText,
		transcript.StepToolCall,
		transcript.StepToolResult,
		transcript.StepAssistantText,
	}
	if len(got.Steps) != len(want) {
		t.Fatalf("steps = %v; want %v", kinds(got), want)
	}
	for i, kind := range want {
		if got.Steps[i].Kind != kind {
			t.Fatalf("steps = %v; want %v", kinds(got), want)
		}
	}
	if got.Steps[0].Text != "run python -c 'print(1)' and report the result" {
		t.Errorf("the turn opened on %q; want the person's prompt", got.Steps[0].Text)
	}
	if got.Steps[0].Entry != "7" {
		t.Errorf("the prompt's entry = %q; want the rollout's own ordinal 7", got.Steps[0].Entry)
	}
	if !strings.Contains(got.Steps[5].Text, "did not retry") {
		t.Errorf("the turn ended on %q; want the assistant's account of the block",
			got.Steps[5].Text)
	}
	if got.EndReason != transcript.EndTurn {
		t.Errorf("end reason = %q; want %q", got.EndReason, transcript.EndTurn)
	}
	if got.Anchors.TurnID != "turn-1" {
		t.Errorf("anchors = %+v; want the turn named", got.Anchors)
	}
	// A blocked call has no rollout entry, so neither anchor can name one.
	if got.Anchors.BlockedCallEntry != "" || got.Anchors.DenialEntry != "" {
		t.Errorf("anchors = %+v; want no entry claimed for the blocked call", got.Anchors)
	}
}

func TestContinuationReadsTheBlockedResultAndTheScriptThatRanIt(t *testing.T) {
	got, err := Continuation(writeTurns(t, blockedTurn), "turn-1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	call := got.Steps[3]
	if call.Tool != "exec" || call.ToolUseID != "call_a" {
		t.Errorf("the call step = %+v; want the exec call named", call)
	}
	// The code-mode script builds its commands, so no single target exists and
	// the script itself is what there is to read.
	if call.Target != "" || !strings.Contains(call.Text, "tools.exec_command") {
		t.Errorf("the call step = %+v; want the script as its text", call)
	}
	result := got.Steps[4]
	if !result.IsError {
		t.Errorf("the result step = %+v; want a call that never reached a process read as failed", result)
	}
	if !strings.Contains(result.Text, "blocked by PreToolUse hook") {
		t.Errorf("the result step = %+v; want the block it carried", result)
	}
}

func TestContinuationEndsAtTheNextTurnWhenTheFileRecordsNoCompletion(t *testing.T) {
	// The completion event is what a finished turn writes; a turn the harness
	// never closed still ends where the next one starts.
	body := strings.ReplaceAll(blockedTurn,
		`{"timestamp":"2026-09-20T16:42:45.500Z","ordinal":16,"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`+"\n", "")
	got, err := Continuation(writeTurns(t, body), "turn-1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if got.EndReason != transcript.EndTurn {
		t.Errorf("end reason = %q; want %q", got.EndReason, transcript.EndTurn)
	}
	if n := len(got.Steps); n != 6 {
		t.Errorf("steps = %v; want the six of turn-1 and none of turn-2", kinds(got))
	}
}

func TestContinuationRunningOutOfFileSaysTranscript(t *testing.T) {
	body := blockedTurn[:strings.Index(blockedTurn, `{"timestamp":"2026-09-20T16:42:45.500Z"`)]
	got, err := Continuation(writeTurns(t, body), "turn-1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if got.EndReason != transcript.EndTranscript {
		t.Errorf("end reason = %q; want %q", got.EndReason, transcript.EndTranscript)
	}
}

func TestContinuationStopsAtTheBudgetAndSaysSo(t *testing.T) {
	got, err := Continuation(writeTurns(t, blockedTurn), "turn-1",
		transcript.ContinuationBudget{Steps: 2, TextBytes: 8})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if len(got.Steps) != 2 || got.EndReason != transcript.EndBudget {
		t.Fatalf("steps = %v ending %q; want 2 and %q",
			kinds(got), got.EndReason, transcript.EndBudget)
	}
	if got.Steps[0].Text != "run pyth" || !got.Steps[0].Truncated {
		t.Errorf("the first step = %q (truncated %t); want the first 8 bytes marked cut",
			got.Steps[0].Text, got.Steps[0].Truncated)
	}
}

func TestContinuationWithoutTheTurnReachesNothingAndNamesWhy(t *testing.T) {
	path := writeTurns(t, blockedTurn)
	got, err := Continuation(path, "turn-absent", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if got.Granularity != transcript.JoinUnavailable ||
		!strings.Contains(got.Reason, "turn-absent") {
		t.Errorf("an absent turn = %q (%s); want unavailable naming the turn",
			got.Granularity, got.Reason)
	}
	if got.Anchors.TranscriptPath != path {
		t.Errorf("anchors = %+v; want the file that was opened", got.Anchors)
	}

	bare, err := Continuation(path, "", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatalf("the reading failed: %v", err)
	}
	if bare.Granularity != transcript.JoinUnavailable || bare.Steps != nil {
		t.Errorf("a row without a turn identifier = %q with %d steps; want unavailable and none",
			bare.Granularity, len(bare.Steps))
	}
}

func TestContinuationOfAnUnreadableTranscriptIsAnError(t *testing.T) {
	// A file that will not open is a limit on the continuation, and the caller
	// distinguishes it from a turn that is simply not there.
	_, err := Continuation(filepath.Join(t.TempDir(), "absent.jsonl"), "turn-1",
		transcript.ContinuationBudget{})
	if err == nil {
		t.Fatal("an absent transcript read as a successful reading")
	}
}
