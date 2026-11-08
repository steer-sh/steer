package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHookContextReplyKeepsGuidanceOutOfTheOperatorLine checks that guidance reaches
// the model's context alone, that operator lines reach both channels, and that a
// reply with neither is not written.
func TestHookContextReplyKeepsGuidanceOutOfTheOperatorLine(t *testing.T) {
	decode := func(raw []byte) (event, system, context string, hasSystem bool) {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("reply is not JSON: %v: %s", err, raw)
		}
		system, hasSystem = doc["systemMessage"].(string)
		hook, _ := doc["hookSpecificOutput"].(map[string]any)
		event, _ = hook["hookEventName"].(string)
		context, _ = hook["additionalContext"].(string)
		return event, system, context, hasSystem
	}

	if _, ok := hookContextReply("SessionStart", nil, ""); ok {
		t.Error("a reply with no line and no guidance must not be written")
	}

	raw, ok := hookContextReply("SubagentStart", nil, "GUIDE")
	if !ok {
		t.Fatal("guidance alone must be written")
	}
	event, _, context, hasSystem := decode(raw)
	if event != "SubagentStart" || context != "GUIDE" || hasSystem {
		t.Errorf("guidance alone: event %q, context %q, systemMessage present %v", event, context, hasSystem)
	}

	raw, ok = hookContextReply("SessionStart", []string{"line one", "line two"}, "GUIDE")
	if !ok {
		t.Fatal("lines with guidance must be written")
	}
	event, system, context, _ := decode(raw)
	if event != "SessionStart" || system != "line one\nline two" {
		t.Errorf("operator line: event %q, systemMessage %q", event, system)
	}
	if !strings.HasPrefix(context, "line one\nline two") || !strings.HasSuffix(context, "GUIDE") {
		t.Errorf("context must carry the lines, then the guidance: %q", context)
	}
	if strings.Contains(system, "GUIDE") {
		t.Errorf("the guidance reached the operator line: %q", system)
	}
}
