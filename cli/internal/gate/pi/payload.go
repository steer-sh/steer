// Package pi adapts Pi extension events to the harness-neutral commit gate.
package pi

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	txpi "github.com/steer-sh/steer-cli/internal/transcript/pi"
)

// Payload is the Claude-shaped envelope written by Pi's commit-gate bridge.
type Payload struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Model          string `json:"model"`
	CWD            string `json:"cwd"`
}

// Output is the bridge reply. The TypeScript bridge consumes AdditionalContext and
// SystemMessage, then returns only Pi's native block fields to the tool_call event.
type Output struct {
	Block             bool   `json:"block,omitempty"`
	Reason            string `json:"reason,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	SystemMessage     string `json:"systemMessage,omitempty"`
}

// Render turns an engine decision into Pi's reply. A clean review never returns an
// explicit allow: undefined is Pi's ordinary continuation contract.
func Render(d gate.Decision) (Output, bool) {
	message := d.SystemMessage
	if d.LedgerError != nil {
		note := "[commit-gate] the adjudication row could NOT be written (" +
			d.LedgerError.Error() + "). The commit is not blocked by this."
		if message == "" {
			message = note
		} else {
			message += " " + note
		}
	}
	switch d.Kind {
	case gate.Deny:
		return Output{Block: true, Reason: d.Reason, SystemMessage: message}, true
	case gate.Allow, gate.ContextOnly:
		// Pi's reply has no approval to grant, so an Allow renders as its receipt.
		return Output{AdditionalContext: d.Reason, SystemMessage: message}, true
	default:
		if message == "" {
			return Output{}, false
		}
		return Output{SystemMessage: message}, true
	}
}

// Run reads a Pi bridge payload, rules on a commit, and writes a native reply. Every
// adapter failure prints nothing so a broken gate cannot block the tool call.
func Run(ctx context.Context, cfg gate.Config, reviewer gate.Reviewer, stdin io.Reader, stdout io.Writer) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	var payload Payload
	if json.Unmarshal(raw, &payload) != nil || payload.HookEventName != "PreToolUse" ||
		payload.ToolName != "Bash" || payload.SessionID == "" {
		return
	}
	command := payload.ToolInput.Command
	if !cc.IsCommitCommand(command) {
		return
	}
	cwd := payload.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if reason := cc.AmbiguousShellReason(command, cwd); reason != "" {
		encoded, err := json.Marshal(Output{Block: true, Reason: reason,
			SystemMessage: cc.AmbiguousShellReceipt})
		if err == nil {
			_, _ = stdout.Write(append(encoded, '\n'))
		}
		return
	}
	repo := cc.RepoDir(command, cwd)
	rowExtra := gate.Row{"client": "pi", "harness": "pi"}
	if payload.TranscriptPath != "" {
		rowExtra["transcript"] = payload.TranscriptPath
		if depth, ok := txpi.ContextTokens(payload.TranscriptPath); ok {
			rowExtra["context_tokens"] = depth
		}
	}
	if payload.Model != "" {
		rowExtra["author_model"] = payload.Model
	}
	for key, value := range cc.RepoFields(repo) {
		rowExtra[key] = value
	}

	engine := gate.NewEngine(cfg, reviewer)
	decision := engine.Handle(ctx, gate.Request{
		Repo: repo, Actor: payload.SessionID, Channel: gate.ChannelPreToolUse,
		SessionID: payload.SessionID,
		Spec:      cc.DiffSpecFor(command),
		Message:   cc.ExtractMessage(command, cwd),
		RerunNote: cc.RerunNote(command),
		RowExtra:  rowExtra,
	})
	out, ok := Render(decision)
	if !ok {
		return
	}
	if encoded, err := json.Marshal(out); err == nil {
		_, _ = stdout.Write(append(encoded, '\n'))
	}
}
