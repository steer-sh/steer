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

// StopBlock reads the task around one Stop block: the turn before the feedback
// entry that carries line, and what the session did after it. A non-empty
// promptID must match that entry's promptId. The context walks parentUuid back
// to the turn's prompt; the continuation walks it forward as Continuation does.
func StopBlock(path, line, promptID string, budget transcript.ContinuationBudget) (transcript.StopBlockReading, error) {
	budget = budget.WithDefaults()
	out := transcript.StopBlockReading{Continuation: transcript.Continuation{
		Harness:     harness.ClaudeCode,
		Granularity: transcript.JoinUnavailable,
		Anchors:     transcript.ContinuationAnchors{TranscriptPath: path, TurnID: promptID},
	}}
	if line == "" {
		out.Reason = "the run recorded no reason line to find its block by"
		return out, nil
	}

	type node struct {
		parent string
		human  bool
	}
	nodes := map[string]node{}
	var root string
	err := eachWalkRow(path, func(r walkRow) {
		if r.IsSidechain || r.UUID == "" {
			return
		}
		_, human := stepsOf(r)
		nodes[r.UUID] = node{parent: r.ParentUUID, human: human}
		if isStopFeedback(r, line) && (promptID == "" || r.PromptID == promptID) {
			out.Matches++
			if root == "" {
				root = r.UUID
			}
		}
	})
	if err != nil {
		return out, err
	}
	if root == "" {
		out.Reason = fmt.Sprintf("the transcript %s records no Stop feedback carrying %q", path, line)
		return out, nil
	}

	// The context is the root's ancestors back to the prompt that opened the
	// turn, or to the start of the chain when no prompt precedes it.
	chain := map[string]bool{}
	for id := nodes[root].parent; id != "" && !chain[id]; id = nodes[id].parent {
		n, ok := nodes[id]
		if !ok {
			break
		}
		chain[id] = true
		if n.human {
			break
		}
	}

	var context []transcript.ContinuationStep
	walk := walker{budget: budget}
	err = eachWalkRow(path, func(r walkRow) {
		switch {
		case r.IsSidechain:
		case chain[r.UUID]:
			steps, _ := stepsOf(r)
			context = append(context, steps...)
		case r.UUID == root && !walk.found:
			walk.start(root)
			out.Granularity = transcript.JoinEntry
			out.Anchors.DenialEntry = root
			steps, _ := stepsOf(r)
			for _, step := range steps {
				if walk.appendStep(&out.Continuation, step) {
					walk.done = true
				}
			}
		case walk.found:
			walk.consider(r, &out.Continuation)
		}
	})
	if err != nil {
		return out, err
	}
	out.Context, out.ContextOmitted = transcript.KeepContext(context, budget)
	if out.EndReason == "" {
		out.EndReason = transcript.EndTranscript
	}
	return out, nil
}

// isStopFeedback reports a Stop hook's feedback entry whose text carries line.
// When several hooks block one Stop, the entry holds every reason.
func isStopFeedback(r walkRow, line string) bool {
	if r.Type != "user" || !r.IsMeta || r.Message == nil {
		return false
	}
	text := plainText(r.Message.Content)
	return strings.HasPrefix(text, stopFeedbackPrefix) && transcript.HasLine(text, line)
}

// eachWalkRow calls fn with each transcript line that decodes, in file order.
func eachWalkRow(path string, fn func(walkRow)) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to read the transcript %s: %w", path, err)
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var r walkRow
			if err := json.Unmarshal(line, &r); err == nil {
				fn(r)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("failed to read the transcript %s: %w", path, readErr)
		}
	}
}
