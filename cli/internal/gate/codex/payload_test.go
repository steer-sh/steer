package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// rootPayload is a PreToolUse event as Codex sends it from the root thread.
const rootPayload = `{"cwd":"/repo","hook_event_name":"PreToolUse","model":"gpt-5.6-sol",` +
	`"permission_mode":"bypassPermissions","session_id":"session-1",` +
	`"tool_input":{"command":"git commit -m x"},"tool_name":"Bash",` +
	`"tool_use_id":"exec-1","transcript_path":"/rollouts/root.jsonl","turn_id":"turn-1"}`

// subagentPayload is the same event from a subagent thread: agent_id names the thread,
// session_id stays the parent's, and transcript_path is the subagent's own rollout.
const subagentPayload = `{"agent_id":"agent-9","agent_type":"default","cwd":"/repo",` +
	`"hook_event_name":"PreToolUse","model":"gpt-5.6-sol","session_id":"session-1",` +
	`"tool_input":{"command":"git commit -m x"},"tool_name":"Bash",` +
	`"transcript_path":"/rollouts/agent.jsonl","turn_id":"turn-2"}`

func TestRenderDenyUsesNativePermissionDecision(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Deny, Reason: "fix it", SystemMessage: "fired"})
	if !ok || out.HookSpecificOutput == nil {
		t.Fatalf("render = %#v, %v", out, ok)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" ||
		out.HookSpecificOutput.PermissionDecisionReason != "fix it" ||
		out.HookSpecificOutput.HookEventName != "PreToolUse" ||
		out.SystemMessage != "fired" {
		t.Fatalf("render = %#v", *out.HookSpecificOutput)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), `"decision"`) {
		t.Fatalf("legacy block field emitted: %s", raw)
	}
}

func TestRenderAllowDoesNotApproveTool(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Allow, SystemMessage: "clean"})
	if !ok || out.HookSpecificOutput != nil || out.SystemMessage != "clean" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
}

func TestRenderContextOnly(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.ContextOnly, Reason: "last review"})
	if !ok || out.HookSpecificOutput == nil {
		t.Fatalf("render = %#v, %v", out, ok)
	}
	if out.HookSpecificOutput.AdditionalContext != "last review" ||
		out.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("render = %#v", *out.HookSpecificOutput)
	}
}

func TestRenderSilentEmitsNothing(t *testing.T) {
	if out, ok := Render(gate.Decision{Kind: gate.Silent}); ok {
		t.Fatalf("silent decision rendered %#v", out)
	}
}

func TestParsePayloadReadsCurrentBashEvent(t *testing.T) {
	got, ok := parsePayload([]byte(rootPayload))
	if !ok {
		t.Fatal("current Codex payload did not parse")
	}
	if got.command != "git commit -m x" || got.cwd != "/repo" || got.actor != "session-1" ||
		got.model != "gpt-5.6-sol" || got.transcript != "/rollouts/root.jsonl" || got.threadID != "" {
		t.Fatalf("parse = %#v", got)
	}
}

func TestParsePayloadAcceptsLegacyExecCommand(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","session_id":"session-1",` +
		`"tool_input":{"cmd":"git commit -m x","workdir":"/legacy"}}`)
	got, ok := parsePayload(raw)
	if !ok || got.command != "git commit -m x" || got.cwd != "/legacy" || got.actor != "session-1" {
		t.Fatalf("parse = %#v, %v", got, ok)
	}
}

func TestSubagentThreadKeysTheChain(t *testing.T) {
	root, ok := parsePayload([]byte(rootPayload))
	if !ok {
		t.Fatal("root payload did not parse")
	}
	sub, ok := parsePayload([]byte(subagentPayload))
	if !ok {
		t.Fatal("subagent payload did not parse")
	}
	if sub.actor != "agent-9" || sub.threadID != "agent-9" || sub.agentType != "default" {
		t.Fatalf("subagent actor = %#v", sub)
	}
	if root.actor == sub.actor {
		t.Fatalf("root and subagent share chain actor %q", root.actor)
	}
	// The root session is provenance on both, so a reader counts the pair as one unit.
	if root.provenanceSessionID() != "session-1" || sub.provenanceSessionID() != "session-1" {
		t.Fatalf("provenance = %q and %q", root.provenanceSessionID(), sub.provenanceSessionID())
	}
	if sub.transcript != "/rollouts/agent.jsonl" {
		t.Fatalf("subagent transcript = %q", sub.transcript)
	}
}

func TestChainActorIsStableAcrossTurns(t *testing.T) {
	first, _ := parsePayload([]byte(rootPayload))
	retry, ok := parsePayload([]byte(strings.Replace(rootPayload, `"turn_id":"turn-1"`, `"turn_id":"turn-9"`, 1)))
	if !ok || first.actor != retry.actor {
		t.Fatalf("chain actors = %q and %q", first.actor, retry.actor)
	}
}

func TestParsePayloadFallsBackToTurnWithoutSession(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","turn_id":"turn-only",` +
		`"tool_input":{"command":"git commit -m x"}}`)
	got, ok := parsePayload(raw)
	if !ok || got.actor != "turn-only" || got.provenanceSessionID() != "turn-only" {
		t.Fatalf("parse = %#v, %v", got, ok)
	}
}

func TestParsePayloadRejectsWrongEventAndTool(t *testing.T) {
	for _, raw := range []string{
		`{"hook_event_name":"Stop","tool_name":"Bash","session_id":"s","tool_input":{"command":"git commit -m x"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"apply_patch","session_id":"s","tool_input":{"command":"git commit -m x"}}`,
	} {
		if got, ok := parsePayload([]byte(raw)); ok {
			t.Fatalf("parse accepted wrong contract: %#v", got)
		}
	}
}
