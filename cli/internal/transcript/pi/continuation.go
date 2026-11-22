package pi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Continuation returns what the session did after a guard blocked one call:
// the toolResult entry carrying toolCallID, and the entries descending from it
// until a person speaks. It follows the branch StopBlock would; each walked
// entry with a child off that branch counts once in BranchPoints.
func Continuation(path, toolCallID string, budget transcript.ContinuationBudget) (transcript.Continuation, error) {
	budget = budget.WithDefaults()
	out := transcript.Continuation{
		Harness:     harness.Pi,
		Granularity: transcript.JoinUnavailable,
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: path,
			CallID:         toolCallID,
		},
	}
	if toolCallID == "" {
		out.Reason = "the audit row carried no call identifier"
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
		if root != "" || e.Type != typeMessage || e.Message == nil {
			continue
		}
		switch {
		case e.Message.Role == roleAssistant && callsTool(e, toolCallID):
			out.Anchors.BlockedCallEntry = e.ID
		case e.Message.Role == roleToolResult && e.Message.ToolCallID == toolCallID:
			root = e.ID
		}
	}
	if root == "" {
		out.Reason = fmt.Sprintf("the transcript %s records no result for call %s", path, toolCallID)
		if nested := NestedCall(toolCallID); nested != "" {
			out.Reason += "; " + nested
		}
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

	out.Granularity = transcript.JoinCall
	out.Anchors.DenialEntry = root
	done := false
	for _, step := range stepsOf(byID[root]) {
		if !done {
			done = out.Append(step, budget)
		}
	}
	reached := map[string]bool{root: true}
	forked := map[string]bool{}
	for _, e := range entries {
		if !reached[e.ParentID] || reached[e.ID] {
			continue
		}
		// Scanning on after the walk stops counts a branch a rewind appended later.
		if !onBranch(branch, e.ID) {
			if !forked[e.ParentID] {
				forked[e.ParentID] = true
				out.BranchPoints++
			}
			continue
		}
		if done {
			continue
		}
		if isHuman(e) {
			// The person's turn ends the reading, so its own forks are not passed.
			out.Append(stepsOf(e)[0], budget)
			out.EndReason, done = transcript.EndHumanTurn, true
			continue
		}
		reached[e.ID] = true
		for _, step := range stepsOf(e) {
			if done = out.Append(step, budget); done {
				break
			}
		}
	}
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// BranchFollowed says which branch a Pi reading follows past a fork.
const BranchFollowed = "the session's active branch, or the block's latest descendant when the session left it"

// NestedCall says why a call another tool made, whose id Pi writes as
// "<parent>/<n>", has no entry to read, and is "" for any other id.
func NestedCall(toolCallID string) string {
	parent, _, nested := strings.Cut(toolCallID, "/")
	if !nested {
		return ""
	}
	return fmt.Sprintf("the call was made inside call %s, and Pi records such a call in no entry", parent)
}

// callsTool reports whether an assistant entry holds the tool call id names.
func callsTool(e walkEntry, id string) bool {
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == blockToolCall && b.ID == id {
			return true
		}
	}
	return false
}
