package codex

import (
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
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
	for key, value := range ledger.TaskFields(actor, cc.AdapterTurns(txcodex.New(), rollout), cfg.MaxTaskChars) {
		fields[key] = value
	}
	return fields
}

func epoch(ts string) *float64 {
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil
	}
	seconds := float64(parsed.UnixNano()) / 1e9
	return &seconds
}
