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
	found, done  bool
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
		if id != "" && id != w.turn {
			w.turn, w.context = id, nil
		}
		if r.Type != typeResponseItem {
			return
		}
		step, ok := stepOf(r, p)
		if carries && (w.turnID == "" || w.turn == w.turnID) {
			out.Matches++
			w.found = true
			out.Granularity = transcript.JoinEntry
			out.Anchors.TurnID = w.turn
			out.Anchors.DenialEntry = step.Entry
			w.done = out.Append(step, w.budget)
			return
		}
		if ok {
			w.context = append(w.context, step)
		}
		return
	}

	if carries && (w.turnID == "" || id == w.turnID) {
		out.Matches++
	}
	if w.done {
		return
	}
	if id != "" && id != w.turn {
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
