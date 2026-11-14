package cc

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Payload is the PreToolUse hook input.
type Payload struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	// PromptID identifies the user prompt whose turn made the call.
	PromptID string `json:"prompt_id"`
	// AgentID names the subagent the call came from, and is absent on the main thread.
	AgentID string `json:"agent_id"`
}

// Output is the hook's reply. Every field is omitted when unset, so a note-only reply
// carries a system message and nothing else.
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

// Render turns an engine Decision into the hook's reply, or ok=false when the gate has
// nothing to say and must print nothing at all. A note-only reply rules on the
// permission deliberately not at all.
func Render(d gate.Decision) (Output, bool) {
	message := d.SystemMessage
	if d.LedgerError != nil {
		// A failure of the ledger append itself has no disk to record on, so it goes to
		// the operator channel, which needs none.
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
		return Output{
			HookSpecificOutput: &hookOutput{
				HookEventName:            "PreToolUse",
				PermissionDecision:       "deny",
				PermissionDecisionReason: d.Reason,
			},
			SystemMessage: message,
		}, true
	case gate.Allow:
		// additionalContext rides beside the decision, so a clean review reaches the
		// agent without costing the commit its auto-approval.
		return Output{
			HookSpecificOutput: &hookOutput{
				HookEventName:      "PreToolUse",
				PermissionDecision: "allow",
				AdditionalContext:  d.Reason,
			},
			SystemMessage: message,
		}, true
	case gate.ContextOnly:
		// additionalContext is the only way to reach the agent without a decision
		// riding along.
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

// Run reads a PreToolUse payload, rules on it, and writes the reply. It returns
// nothing to signal failure: the gate's contract on a `git commit` is to fail open.
func Run(ctx context.Context, cfg gate.Config, reviewer gate.Reviewer, stdin io.Reader, stdout io.Writer) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if payload.ToolName != "Bash" {
		return
	}
	cmd := payload.ToolInput.Command
	if !IsCommitCommand(cmd) {
		return
	}
	session := payload.SessionID
	if session == "" {
		return
	}

	cwd := payload.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if reason, receipt := PreflightDeny(cmd, cwd); reason != "" {
		out, _ := Render(gate.Decision{Kind: gate.Deny, Reason: reason,
			SystemMessage: receipt})
		encoded, err := json.Marshal(out)
		if err == nil {
			_, _ = stdout.Write(append(encoded, '\n'))
		}
		return
	}
	repo := RepoDir(cmd, cwd)

	engine := gate.NewEngine(cfg, reviewer)
	spec := DiffSpecFor(cmd, cwd)

	// The author fields and the repo fields are stamped on every row this request
	// writes. They are gathered once, because reading the transcript is the expensive
	// half and it does not change mid-decision.
	rowExtra := gate.Row{"client": "claude-code", "harness": "claude-code"}
	if payload.PromptID != "" {
		rowExtra["prompt_id"] = payload.PromptID
	}
	// The subagent thread keys the chain. A subagent and its parent commit under one
	// session id, so the session alone would read the subagent's first commit as the
	// parent's retry.
	actor := session
	if payload.AgentID != "" {
		actor = payload.AgentID
		rowExtra["thread_id"] = payload.AgentID
	}
	for key, value := range AuthorFields(engine.Ledger(), cfg, payload.TranscriptPath, session) {
		rowExtra[key] = value
	}
	for key, value := range RepoFields(repo) {
		rowExtra[key] = value
	}

	request := gate.Request{
		Repo:      repo,
		Actor:     actor,
		Channel:   gate.ChannelPreToolUse,
		SessionID: session,
		Spec:      spec,
		Message:   ExtractMessage(cmd, cwd),
		RerunNote: RerunNote(cmd),
		RowExtra:  rowExtra,
		TurnKey:   "prompt_id",
		TurnID:    payload.PromptID,
		ThreadID:  payload.AgentID,
	}
	// The transcript holds only the main thread's ledgers, which are never a subagent's.
	if payload.AgentID == "" {
		request.LedgerHook = func(row gate.Row, state gate.ChainState) {
			attachLedger(row, payload.TranscriptPath, state)
		}
	}
	decision := engine.Handle(ctx, request)

	out, ok := Render(decision)
	if !ok {
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(encoded, '\n'))
}

// RerunNote returns the deny's note for a command whose own steps a deny discards: a
// file it writes, or a `git add` before its commit. Empty for any other command.
func RerunNote(cmd string) string {
	if WritesAFile(cmd) || StagesBeforeCommit(cmd) {
		return gate.RerunNoteText()
	}
	return ""
}

// attachLedger records the agent's own account of the round the retry closes.
//
// Best-effort and usually absent, because the ledger reaches the transcript seconds
// after this hook runs. `ledger_found: false` here means "not yet", never "the agent
// did not report"; the resolver is what actually fills the field.
func attachLedger(row gate.Row, transcript string, state gate.ChainState) {
	row["transcript"] = transcript
	led := ReadLedger(transcript, float64(state.TS), state.Firing)
	if led == nil {
		return
	}
	for key, value := range led {
		row[key] = value
	}
	row["ledger_found"] = true
	// The same field the resolver stamps. Without it a hook-time capture is the one row
	// shape carrying no join, and the resolver skips it forever.
	if led["ledger_round"] != nil {
		row["ledger_join"] = "round"
	} else {
		row["ledger_join"] = "window"
	}
}
