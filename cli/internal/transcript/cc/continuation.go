package cc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// walkRow is a transcript line read for its position in the session tree. The
// streaming reader needs none of these fields, because it normalizes entries
// one at a time and never relates two of them.
type walkRow struct {
	row
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	// PromptID names the turn a user entry belongs to.
	PromptID string `json:"promptId"`
	// DenialKind is what the harness called the denial. It reads
	// "permission-rule" for a hook block and names no hook.
	DenialKind string `json:"toolDenialKind"`
}

// Continuation returns what the session did after a guard blocked one call.
//
// It finds the tool_result carrying toolUseID and walks parentUuid forward from
// it, so the granularity is JoinCall. The reading stops at the person's next
// turn, at the end of the transcript, or at the budget, and says which.
func Continuation(path, toolUseID string, budget transcript.ContinuationBudget) (transcript.Continuation, error) {
	budget = budget.WithDefaults()
	out := transcript.Continuation{
		Harness:     harness.ClaudeCode,
		Granularity: transcript.JoinUnavailable,
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: path,
			CallID:         toolUseID,
		},
	}
	if toolUseID == "" {
		out.Reason = "the audit row carried no call identifier"
		return out, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return out, fmt.Errorf("failed to read the transcript %s: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	walk := walker{toolUseID: toolUseID, budget: budget}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var r walkRow
			if err := json.Unmarshal(line, &r); err == nil {
				walk.consider(r, &out)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return out, fmt.Errorf("failed to read the transcript %s: %w", path, readErr)
		}
	}
	if !walk.found {
		out.Reason = fmt.Sprintf("the transcript %s records no result for call %s", path, toolUseID)
		return out, nil
	}
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// walker holds the forward walk's position. Children always follow their parent
// in an append-only transcript, so one forward pass both finds the denial and
// walks away from it.
type walker struct {
	toolUseID string
	budget    transcript.ContinuationBudget
	found     bool
	// done reports that the walk stopped emitting. The scan runs on to the end
	// of the file, because a rewind appends its branch after the entries the
	// walk already read.
	done bool
	// member holds the entries on the continuation. An entry joins when its
	// parent is a member, so parallel tool results and attachments join too.
	member map[string]bool
	// spoken holds the members that already have an assistant or human child.
	// A second such child is a branch of a rewound session.
	spoken map[string]bool
}

// start makes root the first entry of the continuation.
func (w *walker) start(root string) {
	w.found = true
	w.member = map[string]bool{root: true}
	w.spoken = map[string]bool{}
}

// consider reads one entry into the walk.
func (w *walker) consider(r walkRow, out *transcript.Continuation) {
	if !w.found {
		w.done = w.locate(r, out)
		return
	}
	if r.IsSidechain || !w.member[r.ParentUUID] {
		return
	}
	speaks := speaks(r)
	if speaks && w.spoken[r.ParentUUID] {
		out.BranchPoints++
		return
	}
	if w.done {
		return
	}
	w.member[r.UUID] = true
	if speaks {
		w.spoken[r.ParentUUID] = true
	}
	w.done = w.emit(r, out)
}

// speaks reports an entry that continues the conversation: an assistant entry
// or the person's turn. A tool result, an attachment or a meta entry hangs off
// the conversation without continuing it.
func speaks(r walkRow) bool {
	if r.IsMeta {
		return false
	}
	if r.Type == "assistant" {
		return true
	}
	_, end := stepsOf(r)
	return end != ""
}

// locate finds the two anchors: the entry holding the blocked call, and the
// entry recording the block. The second is the walk's root.
func (w *walker) locate(r walkRow, out *transcript.Continuation) bool {
	if r.IsSidechain || r.Message == nil {
		return false
	}
	blocks, ok := decodeBlocks(r.Message.Content)
	if !ok {
		return false
	}
	for _, b := range blocks {
		switch {
		case b.Type == "tool_use" && b.ID == w.toolUseID:
			out.Anchors.BlockedCallEntry = r.UUID
		case b.Type == "tool_result" && b.ToolUseID == w.toolUseID:
			w.start(r.UUID)
			out.Granularity = transcript.JoinCall
			out.Anchors.DenialEntry = r.UUID
			return w.appendStep(out, transcript.ContinuationStep{
				Kind:      transcript.StepToolResult,
				Timestamp: r.Timestamp,
				Entry:     r.UUID,
				ToolUseID: b.ToolUseID,
				IsError:   b.IsError,
				Text:      resultText(b),
			})
		}
	}
	return false
}

// emit turns one walked entry into steps under the budget, and reports whether
// the walk stops: the budget is spent or the person spoke.
func (w *walker) emit(r walkRow, out *transcript.Continuation) bool {
	steps, end := stepsOf(r)
	for i, step := range steps {
		spent := w.appendStep(out, step)
		if end != "" && i == len(steps)-1 {
			break
		}
		if spent {
			return true
		}
	}
	if end != "" {
		out.EndReason = end
		return true
	}
	return false
}

// stopFeedbackPrefix opens the meta user entry Claude Code writes for a Stop
// hook's block reason.
const stopFeedbackPrefix = "Stop hook feedback:"

// interruptedPrefix opens the text Claude Code records when the person stops a
// turn.
const interruptedPrefix = "[Request interrupted by user"

// stepsOf turns one entry into the steps a reader sees, and names how its last
// step ends the turn: transcript.EndHumanTurn when the person spoke,
// EndInterrupted when the person stopped the turn, "" when it goes on. An entry
// the harness wrote for its own bookkeeping — an attachment, a meta user entry
// other than a Stop hook's feedback — yields none.
func stepsOf(r walkRow) ([]transcript.ContinuationStep, string) {
	if r.Message == nil || (r.Type != "user" && r.Type != "assistant") {
		return nil, ""
	}
	base := transcript.ContinuationStep{Timestamp: r.Timestamp, Entry: r.UUID}
	if r.IsMeta {
		if text := plainText(r.Message.Content); r.Type == "user" && strings.HasPrefix(text, stopFeedbackPrefix) {
			base.Kind, base.Text = transcript.StepHookFeedback, text
			return []transcript.ContinuationStep{base}, ""
		}
		return nil, ""
	}

	// A user entry whose content is plain text is the person speaking.
	var text string
	if err := json.Unmarshal(r.Message.Content, &text); err == nil {
		if r.Type != "user" {
			return nil, ""
		}
		base.Kind, base.Text = transcript.StepHumanTurn, text
		return []transcript.ContinuationStep{base}, turnEnd(text)
	}

	blocks, ok := decodeBlocks(r.Message.Content)
	if !ok {
		return nil, ""
	}
	var steps []transcript.ContinuationStep
	for _, b := range blocks {
		step := base
		switch b.Type {
		case "thinking":
			// Claude Code often writes a thinking block with the text removed
			// and the signature kept, so no thinking step does not mean no
			// reasoning.
			if b.Thinking == "" {
				continue
			}
			step.Kind, step.Text = transcript.StepThinking, b.Thinking
		case "text":
			if r.Type == "user" {
				step.Kind, step.Text = transcript.StepHumanTurn, b.Text
				return append(steps, step), turnEnd(b.Text)
			}
			step.Kind, step.Text = transcript.StepAssistantText, b.Text
		case "tool_use":
			var input toolUseInput
			_ = json.Unmarshal(b.Input, &input)
			target := input.Command
			if target == "" {
				target = input.FilePath
			}
			step.Kind, step.Tool, step.Target, step.ToolUseID =
				transcript.StepToolCall, b.Name, target, b.ID
		case "tool_result":
			step.Kind, step.ToolUseID, step.IsError, step.Text =
				transcript.StepToolResult, b.ToolUseID, b.IsError, resultText(b)
		default:
			continue
		}
		steps = append(steps, step)
	}
	return steps, ""
}

// turnEnd names how a person's text ends the turn.
func turnEnd(text string) string {
	if strings.HasPrefix(text, interruptedPrefix) {
		return transcript.EndInterrupted
	}
	return transcript.EndHumanTurn
}

// plainText is an entry's content as text: the string itself, or its text
// blocks joined. Any other shape yields "".
func plainText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	blocks, _ := decodeBlocks(raw)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// appendStep adds one step under the walk's budget.
func (w *walker) appendStep(out *transcript.Continuation, step transcript.ContinuationStep) bool {
	return out.Append(step, w.budget)
}

// decodeBlocks reads an entry's content blocks. A content field that is a plain
// string, or any other shape, yields none.
func decodeBlocks(raw json.RawMessage) ([]contentBlock, bool) {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}
