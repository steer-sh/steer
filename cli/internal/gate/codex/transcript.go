package codex

import (
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	"github.com/steer-sh/steer-cli/internal/transcript"
	txcodex "github.com/steer-sh/steer-cli/internal/transcript/codex"
)

// AuthorFields is the author side of a Codex round: which model wrote the diff, under
// how much context, from what task, and the rollout they were read from. None survives
// the session, so all are stamped at write time rather than reconstructed later.
func AuthorFields(ledger *gate.Ledger, cfg gate.Config, rollout, actor, model string) gate.Row {
	fields := gate.Row{}
	if model != "" {
		fields["author_model"] = model
	}
	if rollout == "" {
		return fields
	}
	fields["transcript"] = rollout
	lines := cc.ReadTranscript(rollout)
	for key, value := range ledger.SavePrefix(rollout, lines) {
		fields[key] = value
	}
	if depth, ok := contextTokens(lines); ok {
		fields["context_tokens"] = depth
	}
	turns := humanTurns(rollout)
	if len(turns) == 0 {
		return fields
	}
	last := turns[len(turns)-1].Text
	if len(last) > cfg.MaxTaskChars {
		last = last[:cfg.MaxTaskChars]
	}
	fields["task"] = last
	fields["task_turns"] = len(turns)
	for key, value := range ledger.SaveTask(actor, turns) {
		fields[key] = value
	}
	return fields
}

// humanTurns replays one rollout through the transcript adapter and keeps the turns a
// person typed. An unreadable rollout yields none, so the gate fails open.
func humanTurns(rollout string) []gate.Turn {
	var turns []gate.Turn
	session := transcript.Session{Path: rollout}
	_, err := txcodex.New().Stream(session, transcript.Filter{}, func(e transcript.Event) error {
		if e.Kind != transcript.KindHumanTurn || e.Text == "" {
			return nil
		}
		turns = append(turns, gate.Turn{TS: epoch(e.Timestamp), Text: e.Text})
		return nil
	})
	if err != nil {
		return nil
	}
	return turns
}

func epoch(ts string) *float64 {
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil
	}
	seconds := float64(parsed.UnixNano()) / 1e9
	return &seconds
}
