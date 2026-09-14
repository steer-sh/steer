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

// ToolName is the canonical hook tool name Codex reports for a shell command, and the
// matcher registration must use. LegacyToolName is what older builds sent instead.
const (
	ToolName       = "Bash"
	LegacyToolName = "exec_command"
)

type Payload struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command    string `json:"command"`
		Cmd        string `json:"cmd"`
		Workdir    string `json:"workdir"`
		WorkingDir string `json:"working_directory"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	// AgentID names the subagent thread the call came from, and is absent on the root
	// thread. SessionID stays the parent's in both cases.
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
	Model          string `json:"model"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

// DenyReason is the one line Codex prints for a held commit. Codex refuses a deny
// carrying no reason, and it shows the reason to the operator as well as to the model,
// so the review itself rides in additionalContext and this names where it went.
const DenyReason = "[commit-gate] this commit is held for review. The reviewers' full " +
	"output and the reply format are in this turn's hook context: read them there, " +
	"adjudicate every finding, then retry the commit."

// Output is the hook's reply in Codex's native shape.
type Output struct {
	HookSpecificOutput *hookOutput `json:"hookSpecificOutput,omitempty"`
	SystemMessage      string      `json:"systemMessage,omitempty"`
}

type hookOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

type invocation struct {
	command string
	cwd     string
	// workdirGiven says the payload named the directory the command runs in. Codex
	// code mode omits a nested exec_command workdir, so cwd alone is the session root
	// and may not be where the command lands.
	workdirGiven bool
	actor        string
	sessionID    string
	threadID     string
	agentType    string
	model        string
	transcript   string
}

func (i invocation) provenanceSessionID() string {
	if i.sessionID != "" {
		return i.sessionID
	}
	return i.actor
}

func parsePayload(raw []byte) (invocation, bool) {
	var payload Payload
	if json.Unmarshal(raw, &payload) != nil || payload.HookEventName != "PreToolUse" {
		return invocation{}, false
	}
	if payload.ToolName != ToolName && payload.ToolName != LegacyToolName {
		return invocation{}, false
	}
	command := payload.ToolInput.Command
	if command == "" {
		command = payload.ToolInput.Cmd
	}
	// The subagent thread keys the chain. Two threads of one session commit
	// concurrently, and the parent's session id is the same string in both.
	actor := payload.AgentID
	if actor == "" {
		actor = payload.SessionID
	}
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
	workdirGiven := cwd != ""
	if cwd == "" {
		cwd = payload.CWD
	}
	return invocation{
		command:      command,
		cwd:          cwd,
		workdirGiven: workdirGiven,
		actor:        actor,
		sessionID:    payload.SessionID,
		threadID:     payload.AgentID,
		agentType:    payload.AgentType,
		model:        payload.Model,
		transcript:   payload.TranscriptPath,
	}, true
}

// Render turns an engine Decision into the hook's reply, or ok=false when the gate has
// nothing to say. Allow carries no permission decision: an allow skips Codex's own
// approval prompt, and a gate that reviewed cleanly must not also start approving
// commits the operator would have been asked about.
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
		// The review goes in additionalContext, which Codex hands the model and hides
		// from the screen. permissionDecisionReason is printed to both, so it holds a
		// pointer: a whole review echoed there buries the operator's own transcript.
		return Output{
			HookSpecificOutput: &hookOutput{
				HookEventName:            "PreToolUse",
				PermissionDecision:       "deny",
				PermissionDecisionReason: DenyReason,
				AdditionalContext:        d.Reason,
			},
			SystemMessage: message,
		}, true
	case gate.Allow, gate.ContextOnly:
		// An Allow still carries no decision here, for the reason above. It carries its
		// receipt, which is the half Codex can take.
		return Output{
			HookSpecificOutput: &hookOutput{
				HookEventName:     "PreToolUse",
				AdditionalContext: d.Reason,
			},
			SystemMessage: message,
		}, true
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
	if ambiguousRepo(invocation) {
		emit(stdout, Output{HookSpecificOutput: &hookOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: ambiguousRepoReason,
		}, SystemMessage: ambiguousRepoReceipt})
		return
	}
	cwd := invocation.cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if reason, receipt := cc.PreflightDeny(command, cwd); reason != "" {
		emit(stdout, Output{HookSpecificOutput: &hookOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		}, SystemMessage: receipt})
		return
	}
	repo := cc.RepoDir(command, cwd)
	spec := cc.DiffSpecFor(command, cwd)

	engine := gate.NewEngine(cfg, reviewer)
	rowExtra := gate.Row{"client": "codex", "harness": "codex"}
	if invocation.threadID != "" {
		rowExtra["thread_id"] = invocation.threadID
		rowExtra["agent_type"] = invocation.agentType
	}
	for key, value := range AuthorFields(engine.Ledger(), cfg, invocation.transcript, invocation.actor, invocation.model) {
		rowExtra[key] = value
	}
	for key, value := range cc.RepoFields(repo) {
		rowExtra[key] = value
	}

	decision := engine.Handle(ctx, gate.Request{
		Repo: repo, Actor: invocation.actor, Channel: gate.ChannelPreToolUse,
		SessionID: invocation.provenanceSessionID(),
		Spec:      spec,
		Message:   cc.ExtractMessage(command, cwd), RowExtra: rowExtra,
		RerunNote: cc.RerunNote(command),
	})
	out, ok := Render(decision)
	if !ok {
		return
	}
	emit(stdout, out)
}

func emit(stdout io.Writer, out Output) {
	if encoded, err := json.Marshal(out); err == nil {
		_, _ = stdout.Write(append(encoded, '\n'))
	}
}
