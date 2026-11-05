package cc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

func feedback(prompt, reason string) string {
	return fmt.Sprintf(`"type":"user","isMeta":true,"promptId":%q,"message":{"role":"user","content":%q}`,
		prompt, stopFeedbackPrefix+"\n"+reason)
}

func stepKinds(steps []transcript.ContinuationStep) string {
	var out []string
	for _, s := range steps {
		out = append(out, string(s.Kind))
	}
	return strings.Join(out, ",")
}

const blockReason = "check.sh failed (exit 1).\n\nsteer check run: 1a"

// The shape of a real Stop block: the turn before the feedback entry is the
// context back to its prompt, and the harness's attachment and summary carry
// the chain to the reply, which reads two files in parallel before it goes on.
func TestStopBlockReadsTheTurnAroundItsFeedbackEntry(t *testing.T) {
	path := writeWalk(t,
		entry("old", "", humanTurn("an earlier turn")),
		entry("oldsay", "old", assistantText("earlier work")),
		entry("prompt", "oldsay", humanTurn("add the feature")),
		entry("edit", "prompt", assistantCall("t1", "go build")),
		entry("built", "edit", toolResult("t1", "ok", false)),
		entry("done", "built", assistantText("done")),
		entry("fb", "done", feedback("p1", blockReason)),
		`{"uuid":"att","parentUuid":"fb","type":"attachment"}`,
		`{"uuid":"sum","parentUuid":"att","type":"system","subtype":"stop_hook_summary"}`,
		entry("read1", "sum", assistantCall("r1", "cat a")),
		entry("read2", "read1", assistantCall("r2", "cat b")),
		`{"uuid":"att2","parentUuid":"read2","type":"attachment"}`,
		entry("res1", "read1", toolResult("r1", "a", false)),
		entry("res2", "read2", toolResult("r2", "b", false)),
		entry("fix", "res2", assistantText("fixed")),
		entry("next", "fix", humanTurn("thanks")),
	)
	got, err := StopBlock(path, "steer check run: 1a", "p1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Granularity != transcript.JoinEntry || got.Anchors.DenialEntry != "fb" {
		t.Fatalf("reading = %s at %q (%s); want the feedback entry", got.Granularity, got.Anchors.DenialEntry, got.Reason)
	}
	if k := stepKinds(got.Context); k != "human_turn,tool_call,tool_result,assistant_text" {
		t.Errorf("context = %s; want the turn from its prompt", k)
	}
	if k := stepKinds(got.Steps); k != "hook_feedback,tool_call,tool_call,tool_result,tool_result,assistant_text,human_turn" {
		t.Errorf("continuation = %s; want the parallel reads and the reply after them", k)
	}
	if got.EndReason != transcript.EndHumanTurn || got.BranchPoints != 0 || got.Matches != 1 {
		t.Errorf("end %s, branches %d, matches %d; want the person's turn, no branch, one match",
			got.EndReason, got.BranchPoints, got.Matches)
	}
}

// Two blocks in one turn carry different run lines; each reading anchors on its
// own, and the later block is a step of the earlier one's continuation.
func TestStopBlockTellsTwoBlocksOfOneTurnApart(t *testing.T) {
	path := writeWalk(t,
		entry("prompt", "", humanTurn("go")),
		entry("fb1", "prompt", feedback("p1", blockReason)),
		entry("try", "fb1", assistantText("trying again")),
		entry("fb2", "try", feedback("p1", "check.sh failed again.\n\nsteer check run: 1ab")),
		entry("stop", "fb2", assistantText("pre-existing")),
	)
	first, err := StopBlock(path, "steer check run: 1a", "", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := StopBlock(path, "steer check run: 1ab", "", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Anchors.DenialEntry != "fb1" || second.Anchors.DenialEntry != "fb2" {
		t.Fatalf("anchors = %q and %q; want fb1 and fb2", first.Anchors.DenialEntry, second.Anchors.DenialEntry)
	}
	if k := stepKinds(first.Steps); k != "hook_feedback,assistant_text,hook_feedback,assistant_text" {
		t.Errorf("first continuation = %s", k)
	}
	if k := stepKinds(second.Context); k != "human_turn,hook_feedback,assistant_text" {
		t.Errorf("second context = %s; want the first block inside it", k)
	}
}

// A feedback entry of another turn, or no line at all, reaches nothing.
func TestStopBlockReachesNothingOutsideItsTurn(t *testing.T) {
	path := writeWalk(t, entry("prompt", "", humanTurn("go")), entry("fb", "prompt", feedback("p1", blockReason)))
	for name, tc := range map[string][2]string{
		"another turn":  {"steer check run: 1a", "p9"},
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

// Parallel tool calls give an entry a tool result and a further call as
// children. The walk reads both and counts no branch, where a first-child walk
// followed an attachment into a dead end.
func TestContinuationWalksPastParallelToolCalls(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("a1", "denial", assistantCall("p1", "cat a")),
		entry("a2", "a1", assistantCall("p2", "cat b")),
		`{"uuid":"att","parentUuid":"a2","type":"attachment"}`,
		entry("r1", "a1", toolResult("p1", "a", false)),
		entry("r2", "a2", toolResult("p2", "b", false)),
		entry("say", "r2", assistantText("both read")),
	)
	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if k := stepKinds(c.Steps); k != "tool_result,tool_call,tool_call,tool_result,tool_result,assistant_text" {
		t.Errorf("steps = %s; want both results and the reply", k)
	}
	if c.BranchPoints != 0 {
		t.Errorf("branch points = %d; want none", c.BranchPoints)
	}
}

func interruption(text string) string {
	return fmt.Sprintf(`"type":"user","message":{"content":[{"type":"text","text":%q}]}`, text)
}

// The person stopping the turn ends a reading as interrupted, not as the person
// speaking, in a guard continuation and a Stop block reading alike.
func TestAnInterruptionEndsTheReadingAsInterrupted(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("fb", "denial", feedback("p1", blockReason)),
		entry("try", "fb", assistantCall("t2", "go test")),
		entry("stop", "try", interruption("[Request interrupted by user for tool use]")),
	)
	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil || c.EndReason != transcript.EndInterrupted {
		t.Errorf("guard continuation end = %q, %v; want %q", c.EndReason, err, transcript.EndInterrupted)
	}
	r, err := StopBlock(path, "steer check run: 1a", "", transcript.ContinuationBudget{})
	if err != nil || r.EndReason != transcript.EndInterrupted {
		t.Errorf("stop block end = %q, %v; want %q", r.EndReason, err, transcript.EndInterrupted)
	}
}

// A harness that records the same reason twice leaves two entries carrying the
// line; the reading anchors on the first and counts both.
func TestStopBlockCountsARepeatedFeedbackEntry(t *testing.T) {
	path := writeWalk(t,
		entry("prompt", "", humanTurn("go")),
		entry("fb", "prompt", feedback("p1", blockReason)),
		entry("again", "fb", feedback("p1", blockReason)),
		entry("say", "again", assistantText("declining")),
	)
	r, err := StopBlock(path, "steer check run: 1a", "", transcript.ContinuationBudget{})
	if err != nil || r.Anchors.DenialEntry != "fb" || r.Matches != 2 || r.Anchors.TurnID != "p1" {
		t.Errorf("reading anchored on %q in turn %q with %d matches, %v; want fb, its turn p1 and two",
			r.Anchors.DenialEntry, r.Anchors.TurnID, r.Matches, err)
	}
}
