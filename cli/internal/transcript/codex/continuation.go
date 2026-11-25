package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Continuation returns the turn a guard blocked a call in.
//
// A blocked call never reaches a process, so Codex writes no item for it and
// there is nothing in the rollout to walk away from. The reachable join is the
// turn, and the turn is read whole: it opens on the person's prompt, which is
// the task context the block happened in, and runs to the turn's end. Work in
// it did not necessarily follow from the block, so position attributes nothing.
func Continuation(path, turnID string, budget transcript.ContinuationBudget) (transcript.Continuation, error) {
	budget = budget.WithDefaults()
	out := transcript.Continuation{
		Harness:     harness.Codex,
		Granularity: transcript.JoinUnavailable,
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: path,
			TurnID:         turnID,
		},
	}
	if turnID == "" {
		out.Reason = "the audit row carried no turn identifier"
		return out, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return out, fmt.Errorf("failed to read the transcript %s: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	walk := turnWalk{turnID: turnID, budget: budget}
	for !walk.done {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			walk.consider(line, &out)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return out, fmt.Errorf("failed to read the transcript %s: %w", path, readErr)
		}
	}
	if !walk.found {
		out.Reason = fmt.Sprintf("the transcript %s records no turn %s", path, turnID)
		return out, nil
	}
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// continuationRow is a rollout line read for its position in the turn sequence.
// Ordinal is a pointer because a rollout written before Codex numbered its
// lines carries none, and 0 is a line number.
type continuationRow struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Ordinal   *int            `json:"ordinal"`
	Payload   json.RawMessage `json:"payload"`
}

// turnPayload adds the fields only a turn reading needs to the payload union
// the stream reader already decodes.
type turnPayload struct {
	payload
	// TurnID is where an event_msg or a turn record names its turn. A
	// response_item names it inside the passthrough metadata instead.
	TurnID string `json:"turn_id"`
	// Summary is a reasoning item's readable part. The reasoning itself is
	// encrypted, so an item with an empty summary carries nothing to read.
	Summary []contentItem `json:"summary"`
}

// turnID returns the turn a line belongs to, or "" when the line names none.
func (p turnPayload) turnID() string {
	if p.TurnID != "" {
		return p.TurnID
	}
	if p.Meta != nil {
		return p.Meta.TurnID
	}
	return ""
}

// turnWalk holds the reading's position in the file.
type turnWalk struct {
	turnID string
	budget transcript.ContinuationBudget
	found  bool
	done   bool
}

// consider reads one line into the walk.
func (w *turnWalk) consider(line []byte, out *transcript.Continuation) {
	var r continuationRow
	if err := json.Unmarshal(line, &r); err != nil || r.Payload == nil {
		return
	}
	var p turnPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return
	}

	id := p.turnID()
	switch {
	case !w.found:
		if id != w.turnID {
			return
		}
		w.found = true
		out.Granularity = transcript.JoinTurn
	case id != "" && id != w.turnID:
		// The next turn's first line ends this one. A line naming no turn at
		// all belongs to the turn in progress.
		out.EndReason = transcript.EndTurn
		w.done = true
		return
	}

	if end := turnEnd(r, p); end != "" {
		out.EndReason = end
		w.done = true
		return
	}
	if r.Type != typeResponseItem {
		return
	}
	w.done = w.emit(r, p, out)
}

// turnEnd names how a line ends its turn, or "" for a line that does not.
func turnEnd(r continuationRow, p turnPayload) string {
	if r.Type != typeEventMsg {
		return ""
	}
	switch p.Type {
	case "task_complete":
		return transcript.EndTurn
	case "turn_aborted":
		return transcript.EndInterrupted
	}
	return ""
}

// emit turns one response item into a step under the budget, and reports
// whether the budget is spent.
func (w *turnWalk) emit(r continuationRow, p turnPayload, out *transcript.Continuation) bool {
	step, ok := stepOf(r, p)
	if !ok {
		return false
	}
	return out.Append(step, w.budget)
}

// hookPromptOpen and hookPromptClose wrap the message Codex writes for a Stop
// hook's block reason.
const (
	hookPromptOpen  = "<hook_prompt"
	hookPromptClose = "</hook_prompt>"
)

// hookPromptText returns a hook prompt message's reason without its wrapper,
// and whether the message is a hook prompt.
func hookPromptText(p turnPayload) (string, bool) {
	if p.Type != "message" || p.Role != "user" {
		return "", false
	}
	text := strings.TrimSpace(itemText(p.Content))
	if !strings.HasPrefix(text, hookPromptOpen) {
		return "", false
	}
	if i := strings.Index(text, ">"); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSuffix(text, hookPromptClose), true
}

// stepOf turns one response item into the step a reader sees. A developer
// message, an agent message and a reasoning item with no summary are the
// harness talking to itself, and yield none.
func stepOf(r continuationRow, p turnPayload) (transcript.ContinuationStep, bool) {
	step := transcript.ContinuationStep{Timestamp: r.Timestamp}
	if r.Ordinal != nil {
		step.Entry = strconv.Itoa(*r.Ordinal)
	}
	if text, ok := hookPromptText(p); ok {
		step.Kind, step.Text = transcript.StepHookFeedback, text
		return step, true
	}

	switch {
	case p.Type == "message" && p.Role == "assistant":
		step.Kind, step.Text = transcript.StepAssistantText, itemText(p.Content)
	case p.Type == "message" && p.Role == "user":
		// Codex writes several user-role messages the person never typed. Only
		// the typed one is the prompt the turn answers.
		if p.Meta == nil || !contains(p.Meta.ContentItemKinds, "user.text") {
			return step, false
		}
		step.Kind, step.Text = transcript.StepHumanTurn, itemText(p.Content)
	case p.Type == "reasoning":
		text := itemText(p.Summary)
		if text == "" {
			return step, false
		}
		step.Kind, step.Text = transcript.StepThinking, text
	case p.Type == "function_call" || p.Type == "custom_tool_call":
		step.Kind, step.Tool, step.ToolUseID = transcript.StepToolCall, p.Name, p.CallID
		step.Target, _ = callTarget(p.payload)
		if step.Target == "" {
			// A code-mode script names no single subject: the commands are
			// built inside it. The script itself is what the call acted on, so
			// it goes in the text the budget clips rather than in the target.
			step.Text = p.Input
			if step.Text == "" {
				step.Text = p.Args
			}
		}
	case isCallOutput(p.Type):
		step.Kind, step.ToolUseID = transcript.StepToolResult, p.CallID
		step.Text = outputText(p.Output)
		step.IsError = resultFailed(step.Text)
	default:
		return step, false
	}
	return step, true
}
