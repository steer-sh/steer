package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

const (
	typeCustomMessage = "custom_message"
	// CheckBlockType is the customType of the entry Steer's check extension
	// appends at Pi's settle boundary when check.sh blocks.
	CheckBlockType = "steer-check"
	blockThinking  = "thinking"
)

// walkEntry is an entry with the fields a Stop block reading needs.
type walkEntry struct {
	entry
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
}

// StopBlock reads the task around one check block in a Pi session: the turn
// before the steer-check entry that carries line, and the entries that descend
// from it until a person speaks. Pi records a session as a tree, so the
// continuation follows one branch: the active one when the block is on it, and
// otherwise the branch to the block's most recently written descendant.
func StopBlock(path, line string, budget transcript.ContinuationBudget) (transcript.StopBlockReading, error) {
	budget = budget.WithDefaults()
	out := transcript.StopBlockReading{Continuation: transcript.Continuation{
		Harness:     harness.Pi,
		Granularity: transcript.JoinUnavailable,
		Anchors:     transcript.ContinuationAnchors{TranscriptPath: path},
	}}
	if line == "" {
		out.Reason = "the run recorded no reason line to find its block by"
		return out, nil
	}
	entries, err := readWalkEntries(path)
	if err != nil {
		return out, err
	}
	byID := map[string]walkEntry{}
	root := ""
	for _, e := range entries {
		byID[e.ID] = e
		if isCheckBlock(e, line) {
			out.Matches++
			if root == "" {
				root = e.ID
			}
		}
	}
	if root == "" {
		out.Reason = fmt.Sprintf("the transcript %s records no %s entry carrying %q", path, CheckBlockType, line)
		return out, nil
	}
	active, err := ActiveBranch(path)
	if err != nil {
		return out, err
	}
	branch := active
	if active != nil && !onBranch(active, root) {
		branch = latestBranch(entries, byID, root)
	}

	// The context is the block's ancestors back to the prompt that opened the
	// turn, oldest first.
	var chain []walkEntry
	seen := map[string]bool{root: true}
	for id := byID[root].ParentID; id != "" && !seen[id]; {
		e, ok := byID[id]
		if !ok {
			break
		}
		seen[id] = true
		chain = append(chain, e)
		if isHuman(e) {
			break
		}
		id = e.ParentID
	}
	var context []transcript.ContinuationStep
	for i := len(chain) - 1; i >= 0; i-- {
		context = append(context, stepsOf(chain[i])...)
	}

	out.Granularity = transcript.JoinEntry
	out.Anchors.DenialEntry = root
	reached := map[string]bool{root: true}
	done := false
	for _, step := range stepsOf(byID[root]) {
		if !done {
			done = out.Append(step, budget)
		}
	}
	for _, e := range entries {
		if done || !reached[e.ParentID] || reached[e.ID] {
			continue
		}
		if !onBranch(branch, e.ID) {
			continue
		}
		if isHuman(e) {
			out.EndReason = transcript.EndHumanTurn
			break
		}
		reached[e.ID] = true
		for _, step := range stepsOf(e) {
			if done = out.Append(step, budget); done {
				break
			}
		}
	}
	out.Context, out.ContextOmitted = transcript.KeepContext(context, budget)
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// latestBranch is the path from root down to the last entry in file order that
// descends from it, the leaf Pi would resume had that branch stayed active.
func latestBranch(entries []walkEntry, byID map[string]walkEntry, root string) map[string]struct{} {
	under := map[string]bool{root: true}
	leaf := root
	for _, e := range entries {
		if e.ID != root && under[e.ParentID] {
			under[e.ID] = true
			leaf = e.ID
		}
	}
	out := map[string]struct{}{}
	for id := leaf; id != ""; id = byID[id].ParentID {
		if _, seen := out[id]; seen {
			break
		}
		out[id] = struct{}{}
		if id == root {
			break
		}
	}
	return out
}

func readWalkEntries(path string) ([]walkEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the transcript %s: %w", path, err)
	}
	defer file.Close()
	var out []walkEntry
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 {
			var e walkEntry
			if json.Unmarshal(raw, &e) == nil && e.ID != "" && e.Type != typeSession {
				out = append(out, e)
			}
		}
		if readErr == io.EOF {
			return out, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read the transcript %s: %w", path, readErr)
		}
	}
}

func isCheckBlock(e walkEntry, line string) bool {
	return e.Type == typeCustomMessage && e.CustomType == CheckBlockType &&
		transcript.HasLine(plainText(e.Content), line)
}

func isHuman(e walkEntry) bool {
	return e.Type == typeMessage && e.Message != nil && e.Message.Role == roleUser
}

// plainText joins a content field that is a string or a list of text blocks.
func plainText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	text := ""
	for _, b := range blocks {
		if b.Type == blockText {
			text += b.Text
		}
	}
	return text
}

// stepsOf renders one entry as the continuation steps a reader sees.
func stepsOf(e walkEntry) []transcript.ContinuationStep {
	base := transcript.ContinuationStep{Timestamp: e.Timestamp, Entry: e.ID}
	if e.Type == typeCustomMessage {
		if e.CustomType != CheckBlockType {
			return nil
		}
		base.Kind, base.Text = transcript.StepHookFeedback, plainText(e.Content)
		return []transcript.ContinuationStep{base}
	}
	if e.Type != typeMessage || e.Message == nil {
		return nil
	}
	m := e.Message
	switch m.Role {
	case roleUser:
		base.Kind, base.Text = transcript.StepHumanTurn, plainText(m.Content)
		return []transcript.ContinuationStep{base}
	case roleToolResult:
		base.Kind, base.Tool, base.ToolUseID = transcript.StepToolResult, m.ToolName, m.ToolCallID
		base.IsError, base.Text = m.IsError, plainText(m.Content)
		return []transcript.ContinuationStep{base}
	case roleAssistant:
		var blocks []struct {
			block
			Thinking string `json:"thinking"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			return nil
		}
		var steps []transcript.ContinuationStep
		for _, b := range blocks {
			step := base
			switch b.Type {
			case blockThinking:
				step.Kind, step.Text = transcript.StepThinking, b.Thinking
			case blockText:
				step.Kind, step.Text = transcript.StepAssistantText, b.Text
			case blockToolCall:
				step.Kind, step.Tool, step.ToolUseID = transcript.StepToolCall, b.Name, b.ID
				step.Target = callTarget(b.Name, b.Arguments)
			default:
				continue
			}
			steps = append(steps, step)
		}
		return steps
	}
	return nil
}
