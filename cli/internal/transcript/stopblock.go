package transcript

import "strings"

// JoinEntry means the reading is anchored on the entry that recorded a Stop
// block, found by a line only that block's reason carries. The continuation is
// what the session did after that entry.
const JoinEntry = "entry"

// StepHookFeedback is a hook's block reason as the harness handed it to the
// model: a Stop block's own reason, or a later one in the same turn.
const StepHookFeedback StepKind = "hook_feedback"

// EndInterrupted means the harness recorded that the turn was interrupted.
const EndInterrupted = "interrupted"

// DefaultContextSteps is the task context kept before a Stop block when the
// caller leaves the step limit zero.
const DefaultContextSteps = 12

// StopBlockReading is what a transcript shows around one Stop block: the task
// before it and the continuation after it. The embedded Continuation's steps
// start at the block's own feedback entry, whose identifier is the DenialEntry
// anchor.
type StopBlockReading struct {
	Continuation
	// Context is the turn before the block, oldest first: the prompt that opened
	// the turn when the reading reached it, then the steps closest to the block.
	Context []ContinuationStep `json:"context,omitempty"`
	// ContextOmitted counts the steps between the prompt and the kept context
	// that the budget left out.
	ContextOmitted int `json:"context_omitted,omitempty"`
	// Matches counts the entries that carried the block's line. The reading
	// anchors on the first; more than one means the harness recorded the same
	// reason again.
	Matches int `json:"matches,omitempty"`
}

// KeepContext bounds the steps before a block to budget.ContextSteps. It keeps
// the opening prompt when the first step is one, then the steps closest to the
// block, clipping each step's text, and returns how many it left out. A negative
// limit keeps no steps.
func KeepContext(steps []ContinuationStep, budget ContinuationBudget) ([]ContinuationStep, int) {
	budget = budget.WithDefaults()
	limit := budget.ContextSteps
	if limit < 0 {
		return nil, len(steps)
	}
	var kept []ContinuationStep
	omitted := 0
	if len(steps) > limit {
		if steps[0].Kind == StepHumanTurn && limit > 1 {
			kept, steps, limit = append(kept, steps[0]), steps[1:], limit-1
		}
		omitted = len(steps) - limit
		steps = steps[omitted:]
	}
	kept = append(kept, steps...)
	for i := range kept {
		kept[i].Text, kept[i].Truncated = Clip(kept[i].Text, budget.TextBytes)
	}
	return kept, omitted
}

// HasLine reports whether text holds line as a whole line: preceded by the start
// of text or a newline, and followed by the end of text or a line break.
func HasLine(text, line string) bool {
	for from := 0; line != ""; {
		i := strings.Index(text[from:], line)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(line)
		if (start == 0 || text[start-1] == '\n') &&
			(end == len(text) || text[end] == '\n' || text[end] == '\r') {
			return true
		}
		from = start + 1
	}
	return false
}
