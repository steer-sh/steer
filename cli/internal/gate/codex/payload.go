// Package codex adapts Codex PreToolUse events to the harness-neutral gate.
package codex

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
)

type Payload struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Cmd        string `json:"cmd"`
		Command    string `json:"command"`
		Workdir    string `json:"workdir"`
		WorkingDir string `json:"working_directory"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	CWD       string `json:"cwd"`
}

type Output struct {
	Decision          string `json:"decision,omitempty"`
	Reason            string `json:"reason,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	SystemMessage     string `json:"systemMessage,omitempty"`
}

type invocation struct {
	command   string
	cwd       string
	actor     string
	sessionID string
}

func (i invocation) provenanceSessionID() string {
	if i.sessionID != "" {
		return i.sessionID
	}
	return i.actor
}

func parsePayload(raw []byte) (invocation, bool) {
	var payload Payload
	if json.Unmarshal(raw, &payload) != nil || payload.HookEventName != "PreToolUse" ||
		payload.ToolName != "exec_command" {
		return invocation{}, false
	}
	command := payload.ToolInput.Cmd
	if command == "" {
		command = payload.ToolInput.Command
	}
	actor := payload.SessionID
	if actor == "" {
		actor = payload.TurnID
	}
	if actor == "" {
		return invocation{}, false
	}
	cwd := payload.ToolInput.Workdir
	if cwd == "" {
		cwd = payload.ToolInput.WorkingDir
	}
	if cwd == "" {
		cwd = payload.CWD
	}
	return invocation{command: command, cwd: cwd, actor: actor, sessionID: payload.SessionID}, true
}

// Render preserves Codex's normal approval path except for an actual deny.
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
		return Output{Decision: "block", Reason: d.Reason, SystemMessage: message}, true
	case gate.ContextOnly:
		return Output{AdditionalContext: d.Reason, SystemMessage: message}, true
	default:
		if message == "" {
			return Output{}, false
		}
		return Output{SystemMessage: message}, true
	}
}

func Run(ctx context.Context, cfg gate.Config, reviewer gate.Reviewer, stdin io.Reader, stdout io.Writer) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	invocation, ok := parsePayload(raw)
	if !ok {
		return
	}
	command := invocation.command
	if !cc.IsCommitCommand(command) {
		return
	}
	actor := invocation.actor
	cwd := invocation.cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	repo := cc.RepoDir(command, cwd)
	stagedOnly, includeUntracked := cc.DiffSources(command)
	rowExtra := gate.Row{"client": "codex", "harness": "codex"}
	for key, value := range cc.RepoFields(repo) {
		rowExtra[key] = value
	}
	decision := gate.NewEngine(cfg, reviewer).Handle(ctx, gate.Request{
		Repo: repo, Actor: actor, Channel: gate.ChannelPreToolUse,
		SessionID: invocation.provenanceSessionID(),
		Spec:      gate.DiffSpec{StagedOnly: stagedOnly, IncludeUntracked: includeUntracked},
		Message:   cc.ExtractMessage(command, repo), RowExtra: rowExtra,
		RerunNote: rerunNote(command),
	})
	out, ok := Render(decision)
	if !ok {
		return
	}
	if encoded, err := json.Marshal(out); err == nil {
		_, _ = stdout.Write(append(encoded, '\n'))
	}
}

func rerunNote(command string) string {
	if cc.WritesAFile(command) {
		return gate.RerunNoteText()
	}
	return ""
}
