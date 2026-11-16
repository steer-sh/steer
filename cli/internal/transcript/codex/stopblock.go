package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// StopBlock reads the task around one Stop block: the turn before the hook
// prompt that carries line, and the rest of that turn after it. A non-empty
// turnID must match the hook prompt's turn. The scan runs to the end of the file
// so Matches counts every hook prompt carrying line.
func StopBlock(path, line, turnID string, budget transcript.ContinuationBudget) (transcript.StopBlockReading, error) {
	budget = budget.WithDefaults()
	out := transcript.StopBlockReading{Continuation: transcript.Continuation{
		Harness:     harness.Codex,
		Granularity: transcript.JoinUnavailable,
		Anchors:     transcript.ContinuationAnchors{TranscriptPath: path, TurnID: turnID},
	}}
	if line == "" {
		out.Reason = "the run recorded no reason line to find its block by"
		return out, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return out, fmt.Errorf("failed to read the transcript %s: %w", path, err)
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 256*1024)
	walk := stopWalk{line: line, turnID: turnID, budget: budget}
	for {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 {
			walk.consider(raw, &out)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return out, fmt.Errorf("failed to read the transcript %s: %w", path, readErr)
		}
	}
	if !walk.found {
		out.Reason = fmt.Sprintf("the transcript %s records no hook prompt carrying %q", path, line)
		return out, nil
	}
	out.Context, out.ContextOmitted = transcript.KeepContext(walk.context, budget)
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// stopWalk holds the reading's position: the turn in progress and its steps
// until the block is found, then the continuation after it.
type stopWalk struct {
	line, turnID string
	budget       transcript.ContinuationBudget
	turn         string
	context      []transcript.ContinuationStep
	// open holds while turn has not ended; ended names every turn that has, so
	// a later row of one (a usage record) reopens nothing.
	open        bool
	ended       map[string]bool
	found, done bool
	// inTurn holds from the block until its turn ends or another turn's row
	// appears: a hook prompt with no inline id belongs to that turn while it holds.
	inTurn bool
}

func (w *stopWalk) consider(raw []byte, out *transcript.StopBlockReading) {
	var r continuationRow
	if err := json.Unmarshal(raw, &r); err != nil || r.Payload == nil {
		return
	}
	var p turnPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return
	}
	id := p.turnID()
	text, isPrompt := hookPromptText(p)
	carries := r.Type == typeResponseItem && isPrompt && transcript.HasLine(text, w.line)

	if !w.found {
		if w.opens(id) {
			w.turn, w.context, w.open = id, nil, true
		}
		stale := w.stale(id)
		if turnEnd(r, p) != "" && !stale {
			if w.ended == nil {
				w.ended = map[string]bool{}
			}
			w.ended[w.turn], w.open = true, false
		}
		if r.Type != typeResponseItem {
			return
		}
		step, ok := stepOf(r, p)
		// A hook prompt with no id belongs to the turn in progress, if any.
		anchor := id
		if anchor == "" && w.open {
			anchor = w.turn
		}
		if carries && (w.turnID == "" || anchor == w.turnID) {
			if anchor != w.turn {
				w.context = nil
			}
			w.inTurn = w.open && anchor == w.turn
			w.turn = anchor
			out.Matches++
			w.found = true
			out.Granularity = transcript.JoinEntry
			out.Anchors.TurnID = w.turn
			out.Anchors.DenialEntry = step.Entry
			w.done = out.Append(step, w.budget)
			return
		}
		if ok && !stale {
			w.context = append(w.context, step)
		}
		return
	}

	if w.opens(id) {
		w.inTurn = false
	}
	if carries && (w.turnID == "" || id == w.turnID || (id == "" && w.inTurn)) {
		out.Matches++
	}
	if w.stale(id) {
		return
	}
	if turnEnd(r, p) != "" {
		w.inTurn = false
	}
	if w.done {
		return
	}
	if w.opens(id) {
		out.EndReason, w.done = transcript.EndTurn, true
		return
	}
	if end := turnEnd(r, p); end != "" {
		out.EndReason, w.done = end, true
		return
	}
	if r.Type != typeResponseItem {
		return
	}
	if step, ok := stepOf(r, p); ok {
		w.done = out.Append(step, w.budget)
	}
}

// opens reports whether a row's turn id names a turn other than the walk's
// current one that has not already ended.
func (w *stopWalk) opens(id string) bool {
	return id != "" && id != w.turn && !w.ended[id]
}

// stale reports whether a row belongs to a turn that ended before the walk's
// current one: such a row neither ends the current turn nor adds a step to it.
func (w *stopWalk) stale(id string) bool {
	return id != "" && id != w.turn && w.ended[id]
}
