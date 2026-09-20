package firingaudit

import (
	"fmt"
	"os"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/cc"
	"github.com/steer-sh/steer-cli/internal/transcript/codex"
)

// The locator keys the masters write. A key's absence and its empty value are
// different facts, so a reader tests presence.
const (
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
	// Reason says what is missing, when the granularity is JoinUnavailable.
	Reason string
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
		if _, err := os.Stat(path); err != nil {
			// The locators stay on the reach, so a receipt records where the
			// continuation was sought and not found.
			return Reach{Granularity: transcript.JoinUnavailable,
				TranscriptPath: path, CallID: id,
				Reason: fmt.Sprintf("the transcript %s is gone", path)}
		}
		return Reach{Granularity: transcript.JoinCall, TranscriptPath: path, CallID: id}
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
	reach := r.Reach()
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
		return cc.Continuation(reach.TranscriptPath, reach.CallID, budget)
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
	out, err := r.Continuation(budget)
	if err == nil {
		return out
	}
	reach := r.Reach()
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
