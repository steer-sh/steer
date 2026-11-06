package firingaudit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/cc"
	"github.com/steer-sh/steer-cli/internal/transcript/codex"
)

// The locator keys the masters write. A key's absence and its empty value are
// different facts, so a reader tests presence.
const (
	locatorAgentID        = "agent_id"
	locatorToolUseID      = "tool_use_id"
	locatorTranscriptPath = "transcript_path"
	locatorTurnID         = "turn_id"
)

// Reach is the join a row's own locators make possible, decided before any
// transcript is opened.
type Reach struct {
	// Granularity is what a lookup could achieve: transcript.JoinCall,
	// JoinTurn or JoinUnavailable.
	Granularity string
	// TranscriptPath and CallID are what the lookup would read.
	TranscriptPath string
	CallID         string
	TurnID         string
	// AgentID names the Claude Code subagent whose own transcript
	// TranscriptPath is. It is empty when the lookup reads the session file.
	AgentID string
	// Reason says what is missing, when the granularity is JoinUnavailable.
	Reason string
	// Fallback says why a subagent's call is sought in the session transcript
	// rather than the agent's own file. A reading that reaches nothing there
	// adds it to its reason.
	Fallback string
}

// Reach reports whether a continuation can be looked up for this row, and at
// what granularity, without reading the transcript.
//
// A caller uses it to queue work: a receipt whose continuation was never read
// is revisit work only while the evidence is still there to read.
func (r Row) Reach() Reach {
	path := r.Locators[locatorTranscriptPath]
	switch harness.ID(r.Harness) {
	case harness.ClaudeCode:
		id := r.Locators[locatorToolUseID]
		if id == "" || path == "" {
			return Reach{Granularity: transcript.JoinUnavailable,
				Reason: "the row carries no call identifier and transcript path"}
		}
		// A subagent's calls and their results live in the agent's own file,
		// while the row names the session transcript.
		var fallback string
		if agent := r.Locators[locatorAgentID]; agent != "" {
			sub := cc.SubagentTranscript(path, agent)
			_, err := os.Stat(sub)
			if err == nil {
				return Reach{Granularity: transcript.JoinCall,
					TranscriptPath: sub, CallID: id, AgentID: agent}
			}
			if !errors.Is(err, fs.ErrNotExist) {
				// The agent's file may hold the call, so the session file
				// stands in for nothing.
				return Reach{Granularity: transcript.JoinUnavailable,
					TranscriptPath: sub, CallID: id, AgentID: agent,
					Reason: fmt.Sprintf("the subagent %s transcript %s cannot be read: %v", agent, sub, err)}
			}
			fallback = fmt.Sprintf("the subagent %s transcript %s is absent", agent, sub)
		}
		if _, err := os.Stat(path); err != nil {
			// The locators stay on the reach, so a receipt records where the
			// continuation was sought and not found.
			reason := fmt.Sprintf("the transcript %s is gone", path)
			if fallback != "" {
				reason += "; " + fallback
			}
			return Reach{Granularity: transcript.JoinUnavailable,
				TranscriptPath: path, CallID: id, Reason: reason}
		}
		return Reach{Granularity: transcript.JoinCall, TranscriptPath: path, CallID: id, Fallback: fallback}
	case harness.Codex:
		// A blocked Codex call emits no rollout item, so its own call
		// identifier reaches nothing and the turn is the join. The row's
		// tool_use_id is left off the reach because the lookup never reads it.
		turn := r.Locators[locatorTurnID]
		if turn == "" || path == "" {
			return Reach{Granularity: transcript.JoinUnavailable,
				Reason: "the row carries no turn identifier and transcript path"}
		}
		if _, err := os.Stat(path); err != nil {
			return Reach{Granularity: transcript.JoinUnavailable,
				TranscriptPath: path, TurnID: turn,
				Reason: fmt.Sprintf("the transcript %s is gone", path)}
		}
		return Reach{Granularity: transcript.JoinTurn, TranscriptPath: path, TurnID: turn}
	case harness.Pi:
		return Reach{Granularity: transcript.JoinUnavailable,
			TranscriptPath: path,
			Reason:         "the Pi continuation lookup is not implemented"}
	default:
		return Reach{Granularity: transcript.JoinUnavailable,
			Reason: fmt.Sprintf("harness %q has no continuation lookup", r.Harness)}
	}
}

// Continuation reads what the session did after this row's block.
//
// A row whose Reach is JoinUnavailable yields that granularity and its reason
// without opening anything, so an unreachable continuation costs no read.
func (r Row) Continuation(budget transcript.ContinuationBudget) (transcript.Continuation, error) {
	return r.continuationAt(r.Reach(), budget)
}

// continuationAt reads the continuation reach selected.
func (r Row) continuationAt(reach Reach, budget transcript.ContinuationBudget) (transcript.Continuation, error) {
	out := transcript.Continuation{
		Harness:     harness.ID(r.Harness),
		Granularity: transcript.JoinUnavailable,
		Reason:      reach.Reason,
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: reach.TranscriptPath,
			CallID:         reach.CallID,
			TurnID:         reach.TurnID,
		},
	}
	if reach.Granularity == transcript.JoinUnavailable {
		return out, nil
	}
	switch harness.ID(r.Harness) {
	case harness.ClaudeCode:
		if reach.AgentID != "" {
			return cc.SubagentContinuation(reach.TranscriptPath, reach.AgentID, reach.CallID, budget)
		}
		got, err := cc.Continuation(reach.TranscriptPath, reach.CallID, budget)
		if err == nil && !got.Reached() && reach.Fallback != "" {
			got.Reason += "; " + reach.Fallback
		}
		return got, err
	case harness.Codex:
		return codex.Continuation(reach.TranscriptPath, reach.TurnID, budget)
	}
	// Reach reported a granularity for a harness with no reader, which is a
	// disagreement inside this file rather than a fact about the row.
	out.Reason = fmt.Sprintf("harness %q has no continuation reader", r.Harness)
	return out, nil
}

// continuationOrUnavailable reads the continuation and turns a read failure
// into an unavailable join naming it. The row carries the attempted action, so
// a transcript that will not open bounds what can be said about the task after
// the block and settles nothing about the block itself.
func (r Row) continuationOrUnavailable(budget transcript.ContinuationBudget) transcript.Continuation {
	// One reach serves both, so a failure is anchored at the file that failed.
	reach := r.Reach()
	out, err := r.continuationAt(reach, budget)
	if err == nil {
		return out
	}
	return transcript.Continuation{
		Harness:     harness.ID(r.Harness),
		Granularity: transcript.JoinUnavailable,
		Reason:      err.Error(),
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: reach.TranscriptPath,
			CallID:         reach.CallID,
			TurnID:         reach.TurnID,
		},
	}
}

// locatorPromptID is Claude Code's Stop turn key; Codex names its turn turn_id.
const locatorPromptID = "prompt_id"

// Reach reports whether the transcript around this block can be read, and at
// what granularity, without reading it. The run's reason line is the join, so a
// run that recorded no reason with that line reaches nothing.
func (c CheckCase) Reach() Reach {
	path := c.Run.Locators[locatorTranscriptPath]
	line := checks.RunIDLine(c.Run.RunID)
	var turn string
	switch harness.ID(c.Run.Harness) {
	case harness.ClaudeCode:
		turn = c.Run.Locators[locatorPromptID]
	case harness.Codex:
		turn = c.Run.Locators[locatorTurnID]
	default:
		return Reach{Granularity: transcript.JoinUnavailable,
			Reason: fmt.Sprintf("harness %q has no Stop block reader", c.Run.Harness)}
	}
	unavailable := Reach{Granularity: transcript.JoinUnavailable, TranscriptPath: path, TurnID: turn}
	switch {
	case !transcript.HasLine(c.Run.BlockReason, line):
		unavailable.Reason = "the run recorded no block reason ending in its run line"
	case path == "":
		unavailable.Reason = "the run carries no transcript path"
	default:
		if _, err := os.Stat(path); err != nil {
			unavailable.Reason = fmt.Sprintf("the transcript %s is gone", path)
			break
		}
		return Reach{Granularity: transcript.JoinEntry, TranscriptPath: path, TurnID: turn}
	}
	return unavailable
}

// Reading reads the transcript around this block. A reach that is unavailable,
// or a transcript that will not open, yields an unavailable reading naming why.
func (c CheckCase) Reading(budget transcript.ContinuationBudget) transcript.StopBlockReading {
	reach := c.Reach()
	out := transcript.StopBlockReading{Continuation: transcript.Continuation{
		Harness:     harness.ID(c.Run.Harness),
		Granularity: transcript.JoinUnavailable,
		Reason:      reach.Reason,
		Anchors: transcript.ContinuationAnchors{
			TranscriptPath: reach.TranscriptPath,
			TurnID:         reach.TurnID,
		},
	}}
	if reach.Granularity == transcript.JoinUnavailable {
		return out
	}
	line := checks.RunIDLine(c.Run.RunID)
	var reading transcript.StopBlockReading
	var err error
	switch harness.ID(c.Run.Harness) {
	case harness.ClaudeCode:
		reading, err = cc.StopBlock(reach.TranscriptPath, line, reach.TurnID, budget)
	case harness.Codex:
		reading, err = codex.StopBlock(reach.TranscriptPath, line, reach.TurnID, budget)
	}
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	return reading
}
