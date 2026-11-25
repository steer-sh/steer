package cc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// entry writes one transcript line. The fixtures below build a session tree by
// naming each entry's parent, which is the structure the walk reads.
func entry(uuid, parent, body string) string {
	return fmt.Sprintf(`{"uuid":%q,"parentUuid":%q,%s}`, uuid, parent, body)
}

func assistantText(text string) string {
	return fmt.Sprintf(`"type":"assistant","timestamp":"2026-09-20T18:43:06Z","message":{"content":[{"type":"text","text":%q}]}`, text)
}

func assistantCall(id, command string) string {
	return fmt.Sprintf(`"type":"assistant","timestamp":"2026-09-20T18:43:07Z","message":{"content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}`, id, command)
}

func toolResult(id, text string, isError bool) string {
	return fmt.Sprintf(`"type":"user","timestamp":"2026-09-20T18:43:09Z","message":{"content":[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":%q}]}`, id, isError, text)
}

func humanTurn(text string) string {
	return fmt.Sprintf(`"type":"user","timestamp":"2026-09-20T18:44:00Z","message":{"content":%q}`, text)
}

func writeWalk(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
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

func TestContinuationFollowsTheDenialAndStopsAtTheHumanTurn(t *testing.T) {
	path := writeWalk(t,
		entry("a0", "", assistantCall("toolu_earlier", "ls")),
		entry("r0", "a0", toolResult("toolu_earlier", "ok", false)),
		entry("call", "r0", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		// An attachment carries the chain forward and reads as nothing.
		`{"uuid":"att","parentUuid":"denial","type":"attachment"}`,
		entry("say", "att", assistantText("I will use python3 instead.")),
		entry("retry", "say", assistantCall("toolu_retry", "python3 x.py")),
		entry("retried", "retry", toolResult("toolu_retry", "done", false)),
		entry("human", "retried", humanTurn("thanks")),
		entry("after", "human", assistantText("unrelated later work")),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Granularity != transcript.JoinCall {
		t.Fatalf("granularity = %q; want %q (%s)", c.Granularity, transcript.JoinCall, c.Reason)
	}
	if c.Anchors.BlockedCallEntry != "call" || c.Anchors.DenialEntry != "denial" {
		t.Errorf("anchors = %+v; want the blocked call and the denial entry", c.Anchors)
	}
	want := []transcript.StepKind{
		transcript.StepToolResult, transcript.StepAssistantText,
		transcript.StepToolCall, transcript.StepToolResult, transcript.StepHumanTurn,
	}
	if got := kinds(c); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("steps = %v; want %v", got, want)
	}
	if c.EndReason != transcript.EndHumanTurn {
		t.Errorf("end reason = %q; want %q", c.EndReason, transcript.EndHumanTurn)
	}
	if c.Steps[2].Target != "python3 x.py" {
		t.Errorf("retry target = %q; want the retried command", c.Steps[2].Target)
	}
	if !c.Steps[0].IsError {
		t.Error("the denial step lost is_error")
	}
}

func TestContinuationSkipsEntriesOutsideTheChain(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		// A subagent thread and an entry rooted elsewhere both sit in the same
		// file and descend from no entry the walk visited.
		`{"uuid":"sub","parentUuid":"denial","isSidechain":true,`+assistantText("subagent work")+`}`,
		entry("elsewhere", "call", assistantText("a sibling of the denial")),
		entry("next", "denial", assistantText("the real continuation")),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Steps) != 2 || c.Steps[1].Text != "the real continuation" {
		t.Fatalf("steps = %+v; want the denial and the entry descending from it", c.Steps)
	}
	if c.EndReason != transcript.EndTranscript {
		t.Errorf("end reason = %q; want %q", c.EndReason, transcript.EndTranscript)
	}
}

func TestContinuationCountsABranchItDidNotFollow(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("first", "denial", assistantText("the branch the walk follows")),
		entry("second", "denial", assistantText("the branch a rewind abandoned")),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if c.BranchPoints != 1 {
		t.Errorf("branch points = %d; want 1", c.BranchPoints)
	}
	if len(c.Steps) != 2 || c.Steps[1].Text != "the branch the walk follows" {
		t.Errorf("steps = %+v; want the first child in file order", c.Steps)
	}
}

func TestContinuationCountsABranchAppendedAfterItStopped(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("say", "denial", assistantText("the branch the walk follows")),
		entry("human", "say", humanTurn("stop there")),
		// A rewind reattaches to an entry the walk already left, and the
		// harness appends it after the turn the walk read.
		entry("rewound", "denial", assistantText("the branch a rewind took")),
		// The next turn descends from the human turn, which the walk never
		// left, so it is not a branch of the continuation.
		entry("later", "human", assistantText("the next turn")),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if c.EndReason != transcript.EndHumanTurn {
		t.Fatalf("end reason = %q; want %q", c.EndReason, transcript.EndHumanTurn)
	}
	if c.BranchPoints != 1 {
		t.Errorf("branch points = %d; want the rewind after the stop counted", c.BranchPoints)
	}
	if len(c.Steps) != 3 {
		t.Errorf("steps = %+v; want the walk to stop emitting at the human turn", c.Steps)
	}
}

func TestContinuationStopsAtTheStepBudget(t *testing.T) {
	lines := []string{
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
	}
	parent := "denial"
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("n%d", i)
		lines = append(lines, entry(id, parent, assistantText("step")))
		parent = id
	}
	path := writeWalk(t, lines...)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{Steps: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Steps) != 3 {
		t.Errorf("steps = %d; want 3", len(c.Steps))
	}
	if c.EndReason != transcript.EndBudget {
		t.Errorf("end reason = %q; want %q", c.EndReason, transcript.EndBudget)
	}
}

func TestContinuationClipsTextAndSaysSo(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", strings.Repeat("é", 40), true)),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{TextBytes: 9})
	if err != nil {
		t.Fatal(err)
	}
	step := c.Steps[0]
	if !step.Truncated {
		t.Error("a clipped step did not report that it was clipped")
	}
	if step.Text != strings.Repeat("é", 4) {
		t.Errorf("text = %q; want a cut on a rune boundary", step.Text)
	}
}

func TestClipDropsOnlyTheRuneTheLimitSplit(t *testing.T) {
	// A byte that decodes to nothing sits early in the text. Only the rune the
	// limit splits may be dropped, so everything before the limit survives.
	text := "ab\xffcdefgh"
	got, cut := transcript.Clip(text, 6)
	if !cut || got != "ab\xffcde" {
		t.Errorf("Clip = %q, %t; want the first 6 bytes kept", got, cut)
	}
	whole, cut := transcript.Clip(text, len(text))
	if cut || whole != text {
		t.Errorf("Clip of a text within the limit = %q, %t; want it whole", whole, cut)
	}
}

func TestContinuationKeepsOnlyReadableThinking(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("blank", "denial", `"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"sig"}]}`),
		entry("kept", "blank", `"type":"assistant","message":{"content":[{"type":"thinking","thinking":"why the call failed"}]}`),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Steps) != 2 {
		t.Fatalf("steps = %+v; want the denial and the readable thinking", c.Steps)
	}
	if c.Steps[1].Kind != transcript.StepThinking || c.Steps[1].Text != "why the call failed" {
		t.Errorf("step = %+v; want the thinking that carried text", c.Steps[1])
	}
}

func TestContinuationReportsWhatItCouldNotJoin(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_other", "ls")),
		entry("result", "call", toolResult("toolu_other", "ok", false)),
	)

	missing, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Granularity != transcript.JoinUnavailable || missing.Reason == "" {
		t.Errorf("unjoined call = %+v; want unavailable with a reason", missing)
	}
	if missing.EndReason != "" {
		t.Errorf("end reason = %q; want none when nothing was read", missing.EndReason)
	}

	noID, err := Continuation(path, "", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if noID.Granularity != transcript.JoinUnavailable || noID.Reason == "" {
		t.Errorf("row without an identifier = %+v; want unavailable with a reason", noID)
	}

	if _, err := Continuation(filepath.Join(t.TempDir(), "gone.jsonl"), "toolu_blocked",
		transcript.ContinuationBudget{}); err == nil {
		t.Error("a missing transcript returned no error")
	}
}

func TestContinuationSurvivesAnUnparsableLine(t *testing.T) {
	path := writeWalk(t,
		entry("call", "", assistantCall("toolu_blocked", "python x.py")),
		"{ not json",
		entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true)),
		entry("next", "denial", assistantText("the continuation")),
	)

	c, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Steps) != 2 {
		t.Errorf("steps = %+v; want the walk to survive the bad line", c.Steps)
	}
}

// sidechain marks an entry as one subagent's, the way Claude Code writes every
// line of an agent's own file.
func sidechain(agent, line string) string {
	return strings.Replace(line, "{", fmt.Sprintf(`{"isSidechain":true,"agentId":%q,`, agent), 1)
}

func TestSubagentContinuationWalksTheAgentStream(t *testing.T) {
	path := writeWalk(t,
		sidechain("a1", entry("prompt", "", humanTurn("do the task"))),
		sidechain("a1", entry("call", "prompt", assistantCall("toolu_blocked", "python x.py"))),
		sidechain("a1", entry("denial", "call", toolResult("toolu_blocked", "Blocked by hook", true))),
		sidechain("a1", entry("say", "denial", assistantText("I will use python3."))),
		// Another agent's entry and a root entry never join the walk, even
		// under a member's uuid.
		sidechain("a2", entry("other", "say", assistantText("another agent"))),
		entry("root", "say", assistantText("the root conversation")),
		sidechain("a1", entry("retry", "say", assistantCall("toolu_retry", "python3 x.py"))),
		sidechain("a1", entry("rewound", "denial", assistantText("an abandoned branch"))),
		sidechain("a1", entry("retried", "retry", toolResult("toolu_retry", "done", false))),
	)

	c, err := SubagentContinuation(path, "a1", "toolu_blocked", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Granularity != transcript.JoinCall {
		t.Fatalf("granularity = %q (%s); want %q", c.Granularity, c.Reason, transcript.JoinCall)
	}
	if c.Anchors.TranscriptPath != path || c.Anchors.BlockedCallEntry != "call" || c.Anchors.DenialEntry != "denial" {
		t.Errorf("anchors = %+v; want the agent file, its blocked call and denial", c.Anchors)
	}
	want := []string{"denial", "say", "retry", "retried"}
	var got []string
	for _, s := range c.Steps {
		got = append(got, s.Entry)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v; want %v", got, want)
	}
	if c.BranchPoints != 1 {
		t.Errorf("branch points = %d; want the rewound branch counted", c.BranchPoints)
	}
	if c.EndReason != transcript.EndTranscript {
		t.Errorf("end reason = %q; want %q", c.EndReason, transcript.EndTranscript)
	}

	if root, err := Continuation(path, "toolu_blocked", transcript.ContinuationBudget{}); err != nil || root.Reached() {
		t.Errorf("the root reading of an agent file = %+v (%v); want it unreached", root, err)
	}
}

func TestSubagentTranscriptSitsBesideTheSession(t *testing.T) {
	got := SubagentTranscript("/p/proj/sess.jsonl", "a1")
	if want := "/p/proj/sess/subagents/agent-a1.jsonl"; got != want {
		t.Errorf("subagent transcript = %q; want %q", got, want)
	}
}
