package transcript

import "github.com/steer-sh/steer-cli/internal/harness"

// How closely a retrieved continuation is tied to the block it follows. A
// receipt records the granularity it achieved rather than the harness's name,
// because two harnesses reach different granularity and one harness can fail to
// reach its own.
const (
	// JoinCall means the continuation follows the blocked call itself: the
	// harness recorded the denial as its own entry and the entries after it
	// descend from that entry.
	JoinCall = "call"
	// JoinTurn means the continuation is the turn the block happened in. The
	// turn holds work that did not follow from the block, so a reading of it
	// attributes nothing to the block by position alone.
	JoinTurn = "turn"
	// JoinUnavailable means no continuation was reached. It is a fact about the
	// evidence, never a reading of the block: an action can be judged with no
	// continuation at all.
	JoinUnavailable = "unavailable"
)

// StepKind names what one continuation step is. It is separate from Kind
// because a continuation is read rather than counted: the assistant's own
// narrative is the part that says what the block did to the task, and no
// classifier reads it.
type StepKind string

const (
	// StepThinking is the assistant's reasoning, recorded by harnesses that keep it.
	StepThinking StepKind = "thinking"
	// StepAssistantText is what the assistant said to the person.
	StepAssistantText StepKind = "assistant_text"
	// StepToolCall is a tool the assistant asked for after the block.
	StepToolCall StepKind = "tool_call"
	// StepToolResult is what a tool returned. The denial itself is one of these.
	StepToolResult StepKind = "tool_result"
	// StepHumanTurn is text a person typed, which ends the continuation.
	StepHumanTurn StepKind = "human_turn"
)

// Why a continuation stopped where it did. A reader needs this to tell an
// exhausted budget from a finished turn: one leaves work unread and the other
// does not.
const (
	// EndHumanTurn means the person spoke, so the turn holding the block ended.
	EndHumanTurn = "human_turn"
	// EndTranscript means the transcript ran out. The session may still be live.
	EndTranscript = "transcript"
	// EndBudget means the step budget ran out with entries still to read.
	EndBudget = "budget"
)

// ContinuationAnchors are the transcript positions a reading rests on, so a
// receipt can name what was opened and a later reader can open the same thing.
type ContinuationAnchors struct {
	// TranscriptPath is the file read, as the harness named it.
	TranscriptPath string `json:"transcript_path,omitempty"`
	// CallID is the identifier the audit row carried, used to find the block.
	CallID string `json:"call_id,omitempty"`
	// BlockedCallEntry is the entry holding the call the guard blocked.
	BlockedCallEntry string `json:"blocked_call_entry,omitempty"`
	// DenialEntry is the entry recording the block, and the walk's root.
	DenialEntry string `json:"denial_entry,omitempty"`
	// TurnID names the turn, for a harness that joins at turn granularity.
	TurnID string `json:"turn_id,omitempty"`
}

// ContinuationStep is one entry of what happened after the block.
type ContinuationStep struct {
	Kind      StepKind `json:"kind"`
	Timestamp string   `json:"timestamp,omitempty"`
	// Entry is the harness's own identifier for the entry this step came from.
	Entry string `json:"entry,omitempty"`
	// Tool, Target and ToolUseID describe a call or a result.
	Tool      string `json:"tool,omitempty"`
	Target    string `json:"target,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Text      string `json:"text,omitempty"`
	// Truncated reports that Text was cut to the budget, so a reader knows the
	// step is a excerpt rather than the whole of what the harness recorded.
	Truncated bool `json:"truncated,omitempty"`
}

// ContinuationBudget bounds one reading. A reader that hits either limit says
// so through EndBudget and Truncated.
type ContinuationBudget struct {
	// Steps is the maximum number of steps to return, counting the denial.
	Steps int
	// TextBytes is the maximum text kept per step.
	TextBytes int
}

// Default step and text limits, applied when a caller leaves the field zero.
const (
	DefaultContinuationSteps     = 24
	DefaultContinuationTextBytes = 2000
)

// WithDefaults fills the unset limits. A negative limit is left alone, which
// lets a caller ask for no text at all.
func (b ContinuationBudget) WithDefaults() ContinuationBudget {
	if b.Steps == 0 {
		b.Steps = DefaultContinuationSteps
	}
	if b.TextBytes == 0 {
		b.TextBytes = DefaultContinuationTextBytes
	}
	return b
}

// Continuation is what the session did after a guard blocked a call.
//
// It answers whether useful work continued. It does not answer whether the
// block was correct: the audit row carries the attempted action, and that
// question is settled from the action in its original task context.
type Continuation struct {
	Harness harness.ID `json:"harness,omitempty"`
	// Granularity is one of JoinCall, JoinTurn or JoinUnavailable.
	Granularity string              `json:"granularity"`
	Anchors     ContinuationAnchors `json:"anchors"`
	Steps       []ContinuationStep  `json:"steps,omitempty"`
	// EndReason says why the reading stopped. It is empty when Granularity is
	// JoinUnavailable.
	EndReason string `json:"end_reason,omitempty"`
	// BranchPoints counts the entries the walk passed that had another child
	// besides the one it followed. A session that was rewound records both
	// branches, and this reading follows the first in file order, so a non-zero
	// count marks a continuation that may be an abandoned branch.
	BranchPoints int `json:"branch_points,omitempty"`
	// Reason says why no continuation was reached, when none was.
	Reason string `json:"reason,omitempty"`
}

// Reached reports that a continuation was retrieved at some granularity.
func (c Continuation) Reached() bool {
	return c.Granularity != "" && c.Granularity != JoinUnavailable
}
