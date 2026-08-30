package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func TestRenderUsesCodexBlockContract(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Deny, Reason: "fix it", SystemMessage: "fired"})
	if !ok || out.Decision != "block" || out.Reason != "fix it" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "permissionDecision") || strings.Contains(string(raw), "hookSpecificOutput") {
		t.Fatalf("Claude fields leaked into Codex output: %s", raw)
	}
}

func TestRenderAllowDoesNotApproveTool(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Allow, SystemMessage: "clean"})
	if !ok || out.Decision != "" || out.SystemMessage != "clean" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
}

func TestRenderContextOnly(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.ContextOnly, Reason: "last review"})
	if !ok || out.AdditionalContext != "last review" || out.Decision != "" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
}

func TestParsePayloadCodexFields(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","turn_id":"turn-1","session_id":"session-1","tool_input":{"cmd":"git commit -m x","workdir":"/repo"}}`)
	got, ok := parsePayload(raw)
	if !ok || got.command != "git commit -m x" || got.cwd != "/repo" || got.actor != "session-1" || got.sessionID != "session-1" {
		t.Fatalf("parse = %#v, %v", got, ok)
	}
}

func TestParsePayloadFallbackFields(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","session_id":"session-1","cwd":"/payload","tool_input":{"command":"git commit -m x","working_directory":"/request"}}`)
	got, ok := parsePayload(raw)
	if !ok || got.command != "git commit -m x" || got.cwd != "/request" || got.actor != "session-1" {
		t.Fatalf("parse = %#v, %v", got, ok)
	}
}

func TestParsePayloadFallsBackToTurnWithoutSession(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","turn_id":"turn-only","tool_input":{"cmd":"git commit -m x"}}`)
	got, ok := parsePayload(raw)
	if !ok || got.actor != "turn-only" || got.provenanceSessionID() != "turn-only" {
		t.Fatalf("parse = %#v, %v", got, ok)
	}
}

func TestRequestIdentityUsesSessionAcrossTurns(t *testing.T) {
	first, ok := parsePayload([]byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","session_id":"session-1","turn_id":"turn-1","tool_input":{"cmd":"git commit -m x"}}`))
	if !ok {
		t.Fatal("first turn did not parse")
	}
	retry, ok := parsePayload([]byte(`{"hook_event_name":"PreToolUse","tool_name":"exec_command","session_id":"session-1","turn_id":"turn-2","tool_input":{"cmd":"git commit -m x"}}`))
	if !ok {
		t.Fatal("retry turn did not parse")
	}
	if first.actor != "session-1" || retry.actor != first.actor {
		t.Fatalf("chain actors = %q and %q, want stable session-1", first.actor, retry.actor)
	}

	withSession := invocation{actor: first.actor, sessionID: "session-1"}
	if withSession.provenanceSessionID() != "session-1" {
		t.Fatalf("session provenance = %q, want session-1", withSession.provenanceSessionID())
	}
	withoutSession := invocation{actor: "turn-only"}
	if withoutSession.provenanceSessionID() != "turn-only" {
		t.Fatalf("session fallback = %q, want turn-only", withoutSession.provenanceSessionID())
	}
}

func TestParsePayloadRejectsWrongEventAndTool(t *testing.T) {
	for _, raw := range []string{
		`{"hook_event_name":"Stop","tool_name":"exec_command","session_id":"s","tool_input":{"cmd":"git commit -m x"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"view_image","session_id":"s","tool_input":{"cmd":"git commit -m x"}}`,
	} {
		if got, ok := parsePayload([]byte(raw)); ok {
			t.Fatalf("parse accepted wrong contract: %#v", got)
		}
	}
}
