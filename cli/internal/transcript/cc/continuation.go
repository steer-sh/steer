package cc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

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
	// frontier is the entry the next step must descend from.
	frontier string
	// followed are the entries the walk already left. A later entry naming one
	// of them as its parent is a second branch of a rewound session.
	followed map[string]bool
}

// consider reads one entry into the walk.
func (w *walker) consider(r walkRow, out *transcript.Continuation) {
	if !w.found {
		w.done = w.locate(r, out)
		return
	}
	if r.IsSidechain {
		return
	}
	if w.done || r.ParentUUID != w.frontier {
		if w.followed[r.ParentUUID] {
			out.BranchPoints++
		}
		return
	}
	w.followed[w.frontier] = true
	w.frontier = r.UUID
	w.done = w.emit(r, out)
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
			w.found = true
			w.frontier = r.UUID
			w.followed = map[string]bool{}
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

// emit turns one walked entry into steps. An entry the harness wrote for its
// own bookkeeping — an attachment, a meta user entry — carries the chain
// forward and contributes nothing to read.
func (w *walker) emit(r walkRow, out *transcript.Continuation) bool {
	if r.Message == nil || r.IsMeta || (r.Type != "user" && r.Type != "assistant") {
		return false
	}
	base := transcript.ContinuationStep{Timestamp: r.Timestamp, Entry: r.UUID}

	// A user entry whose content is plain text is the person speaking, which
	// ends the turn the block happened in.
	var text string
	if err := json.Unmarshal(r.Message.Content, &text); err == nil {
		if r.Type != "user" {
			return false
		}
		step := base
		step.Kind, step.Text = transcript.StepHumanTurn, text
		w.appendStep(out, step)
		out.EndReason = transcript.EndHumanTurn
		return true
	}

	blocks, ok := decodeBlocks(r.Message.Content)
	if !ok {
		return false
	}
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
				w.appendStep(out, step)
				out.EndReason = transcript.EndHumanTurn
				return true
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
		if w.appendStep(out, step) {
			return true
		}
	}
	return false
}

// appendStep adds one step and reports whether the budget is now spent.
func (w *walker) appendStep(out *transcript.Continuation, step transcript.ContinuationStep) bool {
	step.Text, step.Truncated = clip(step.Text, w.budget.TextBytes)
	out.Steps = append(out.Steps, step)
	if len(out.Steps) >= w.budget.Steps {
		out.EndReason = transcript.EndBudget
		return true
	}
	return false
}

// clip cuts text to a byte limit, and reports that it cut. Only a rune the
// limit split is dropped; bytes earlier in the text are kept as they are,
// whether or not they decode.
func clip(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", text != ""
	}
	if len(text) <= limit {
		return text, false
	}
	cut := text[:limit]
	// A rune is at most 4 bytes, so a split one costs at most 3 of them.
	for i := 0; i < utf8.UTFMax-1 && len(cut) > 0; i++ {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut, true
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
