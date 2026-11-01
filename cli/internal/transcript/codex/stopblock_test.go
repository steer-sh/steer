package codex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

func rolloutLine(ordinal int, typ, payload string) string {
	return fmt.Sprintf(`{"timestamp":"2026-09-23T10:00:%02d.000Z","ordinal":%d,"type":%q,"payload":%s}`, ordinal, ordinal, typ, payload)
}

func item(ordinal int, turn, body string) string {
	return rolloutLine(ordinal, typeResponseItem, fmt.Sprintf(`{%s,"internal_chat_message_metadata_passthrough":{"turn_id":%q,"content_item_kinds":["unknown"]}}`, body, turn))
}

func typed(ordinal int, turn, text string) string {
	return rolloutLine(ordinal, typeResponseItem, fmt.Sprintf(`{"type":"message","role":"user","content":[{"type":"input_text","text":%q}],"internal_chat_message_metadata_passthrough":{"turn_id":%q,"content_item_kinds":["user.text"]}}`, text, turn))
}

func said(ordinal int, turn, text string) string {
	return item(ordinal, turn, fmt.Sprintf(`"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]`, text))
}

func hookPrompt(ordinal int, turn, reason string) string {
	return item(ordinal, turn, fmt.Sprintf(`"type":"message","role":"user","content":[{"type":"input_text","text":%q}]`,
		`<hook_prompt hook_run_id="stop:0:/h/hooks.json">`+reason+`</hook_prompt>`))
}

func event(ordinal int, turn, typ string) string {
	return rolloutLine(ordinal, typeEventMsg, fmt.Sprintf(`{"type":%q,"turn_id":%q}`, typ, turn))
}

func stopKinds(steps []transcript.ContinuationStep) string {
	var out []string
	for _, s := range steps {
		out = append(out, string(s.Kind))
	}
	return strings.Join(out, ",")
}

const reason1 = "check.sh failed (exit 1).\n\nsteer check run: 1a"

// The block's own hook prompt anchors the reading: the turn before it is the
// context, what follows in the turn is the continuation, and a second block in
// the same turn is a step of it, not the anchor.
func TestStopBlockReadsTheTurnAroundItsOwnHookPrompt(t *testing.T) {
	path := writeTurns(t, strings.Join([]string{
		typed(1, "t0", "an earlier turn"),
		event(2, "t0", "task_complete"),
		typed(3, "t1", "add the feature"),
		said(4, "t1", "done"),
		hookPrompt(5, "t1", reason1),
		rolloutLine(6, typeEventMsg, `{"type":"item_completed","turn_id":"t1","item":{"type":"HookPrompt","fragments":[{"text":"steer check run: 1a"}]}}`),
		said(7, "t1", "fixing the test"),
		hookPrompt(8, "t1", "check.sh failed again.\n\nsteer check run: 1ab"),
		said(9, "t1", "the failure is pre-existing"),
		event(10, "t1", "task_complete"),
		typed(11, "t2", "a later turn"),
	}, "\n")+"\n")

	got, err := StopBlock(path, "steer check run: 1a", "", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Granularity != transcript.JoinEntry || got.Anchors.DenialEntry != "5" || got.Anchors.TurnID != "t1" {
		t.Fatalf("reading = %s at entry %q turn %q (%s); want entry 5 of t1",
			got.Granularity, got.Anchors.DenialEntry, got.Anchors.TurnID, got.Reason)
	}
	if k := stopKinds(got.Context); k != "human_turn,assistant_text" {
		t.Errorf("context = %s; want the turn's prompt and reply only", k)
	}
	if k := stopKinds(got.Steps); k != "hook_feedback,assistant_text,hook_feedback,assistant_text" {
		t.Errorf("continuation = %s", k)
	}
	if got.Steps[0].Text != reason1 {
		t.Errorf("feedback text = %q; want the reason without its wrapper", got.Steps[0].Text)
	}
	if got.EndReason != transcript.EndTurn || got.Matches != 1 {
		t.Errorf("end %s, matches %d; want the turn's end and one match (the item_completed echo is no entry)",
			got.EndReason, got.Matches)
	}
}

// An interrupted turn ends the reading as interrupted, and a repeated hook
// prompt carrying the same line is counted within the turn the run named.
func TestStopBlockReportsAnInterruptionAndARepeatedReason(t *testing.T) {
	path := writeTurns(t, strings.Join([]string{
		typed(1, "t1", "go"),
		hookPrompt(2, "t1", reason1),
		hookPrompt(3, "t1", reason1),
		event(4, "t1", "turn_aborted"),
		typed(5, "t2", "resume"),
		hookPrompt(6, "t2", reason1),
	}, "\n")+"\n")
	got, err := StopBlock(path, "steer check run: 1a", "t1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if got.EndReason != transcript.EndInterrupted || got.Matches != 2 {
		t.Errorf("end %s, matches %d; want interrupted and the two of turn t1", got.EndReason, got.Matches)
	}
	if got, _ := StopBlock(path, "steer check run: 1a", "", transcript.ContinuationBudget{}); got.Matches != 3 {
		t.Errorf("matches with no turn = %d; want every entry carrying the line", got.Matches)
	}
}

// A hook prompt with no inline turn id belongs to the turn in progress when it
// appears: it counts inside the named turn and not after that turn is over,
// including once the budget has ended the reading.
func TestStopBlockCountsAHookPromptWithNoTurnIDByItsPosition(t *testing.T) {
	bare := func(ordinal int) string {
		return rolloutLine(ordinal, typeResponseItem, fmt.Sprintf(`{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}`,
			`<hook_prompt hook_run_id="stop:0:/h/hooks.json">`+reason1+`</hook_prompt>`))
	}
	for name, tc := range map[string]struct {
		budget transcript.ContinuationBudget
		lines  []string
		want   int
	}{
		"later in the turn": {
			lines: []string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1), said(3, "t1", "fixing"), bare(4), event(5, "t1", "task_complete")},
			want:  2,
		},
		"past a spent budget": {
			budget: transcript.ContinuationBudget{Steps: 1},
			lines:  []string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1), said(3, "t1", "fixing"), bare(4), event(5, "t1", "task_complete")},
			want:   2,
		},
		"after the turn ended": {
			lines: []string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1), event(3, "t1", "task_complete"), bare(4)},
			want:  1,
		},
		"after a usage row of the ended turn": {
			lines: []string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1), event(3, "t1", "task_complete"), rolloutLine(4, "token_usage_record", `{"turn_id":"t1","usage":{}}`), bare(5)},
			want:  1,
		},
		"in a later turn": {
			lines: []string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1), typed(3, "t2", "next"), bare(4)},
			want:  1,
		},
	} {
		path := writeTurns(t, strings.Join(tc.lines, "\n")+"\n")
		got, err := StopBlock(path, "steer check run: 1a", "t1", tc.budget)
		if err != nil {
			t.Fatal(err)
		}
		if got.Matches != tc.want {
			t.Errorf("%s: matches = %d; want %d", name, got.Matches, tc.want)
		}
	}
}

// A turn that is not the run's, or a line no hook prompt carries, reaches nothing.
func TestStopBlockReachesNothingOutsideItsTurn(t *testing.T) {
	path := writeTurns(t, strings.Join([]string{typed(1, "t1", "go"), hookPrompt(2, "t1", reason1)}, "\n")+"\n")
	for name, tc := range map[string][2]string{
		"another turn":  {"steer check run: 1a", "t9"},
		"a longer id":   {"steer check run: 1", ""},
		"no run's line": {"", ""},
	} {
		got, err := StopBlock(path, tc[0], tc[1], transcript.ContinuationBudget{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Reached() || got.Reason == "" {
			t.Errorf("%s: reading %s with reason %q; want unavailable, saying why", name, got.Granularity, got.Reason)
		}
	}
}

// A guard's turn reading ends where the turn was interrupted, not at the next turn.
func TestContinuationEndsAtAnInterruption(t *testing.T) {
	path := writeTurns(t, strings.Join([]string{
		typed(1, "t1", "go"),
		said(2, "t1", "working"),
		event(3, "t1", "turn_aborted"),
		typed(4, "t2", "next"),
	}, "\n")+"\n")
	got, err := Continuation(path, "t1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if got.EndReason != transcript.EndInterrupted {
		t.Errorf("end = %s; want %s", got.EndReason, transcript.EndInterrupted)
	}
}
