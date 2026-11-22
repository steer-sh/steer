package pi

import (
	"regexp"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	txpi "github.com/steer-sh/steer-cli/internal/transcript/pi"
)

// skillBlock matches the block Pi writes in place of a `/skill:<name>` command it
// resolved: the skill's whole body, which the person never typed. The arguments they
// typed after the command follow it.
var skillBlock = regexp.MustCompile(`^<skill name="[^"]*" location="[^"]*">\n(?s:.*?)\n</skill>`)

// AuthorFields is the author side of a Pi round: which model wrote the diff, under how
// much context, from what task, and the session file they were read from. None
// survives the session, so all are stamped at write time.
func AuthorFields(ledger *gate.Ledger, cfg gate.Config, path, session, model string) gate.Row {
	fields := gate.Row{}
	if model != "" {
		fields["author_model"] = model
	}
	if path == "" {
		return fields
	}
	fields["transcript"] = path
	for key, value := range ledger.SavePrefix(path, cc.ReadTranscript(path)) {
		fields[key] = value
	}
	if depth, ok := txpi.ContextTokens(path); ok {
		fields["context_tokens"] = depth
	}
	for key, value := range ledger.TaskFields(session, humanTurns(path), cfg.MaxTaskChars) {
		fields[key] = value
	}
	return fields
}

// humanTurns keeps the active branch's user messages with a resolved skill's body
// removed; a turn that was only the skill command is dropped.
func humanTurns(path string) []gate.Turn {
	var turns []gate.Turn
	for _, turn := range cc.AdapterTurns(txpi.New(), path) {
		turn.Text = strings.TrimSpace(skillBlock.ReplaceAllString(turn.Text, ""))
		if turn.Text != "" {
			turns = append(turns, turn)
		}
	}
	return turns
}
