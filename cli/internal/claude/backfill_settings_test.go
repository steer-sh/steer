package claude

import (
	"strings"
	"testing"
)

// Registration cases for the resolver's Stop hook. The load-bearing property is that
// sibling Stop hooks — steer does not own them — survive register and remove untouched,
// including keeping no matcher key.

func stopGroup(t *testing.T) map[string]any {
	t.Helper()
	settings := readSettings(t)
	hooks, _ := settings["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	if len(stop) == 0 {
		return nil
	}
	group, _ := stop[0].(map[string]any)
	return group
}

func stopHooks(t *testing.T) []any {
	t.Helper()
	group := stopGroup(t)
	if group == nil {
		return nil
	}
	list, _ := group["hooks"].([]any)
	return list
}

func TestRegisterBackfillHookCreatesStopGroup(t *testing.T) {
	withProjectDir(t)
	if err := RegisterBackfillHook(ProjectScope, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	armed, timeout := BackfillHookArmed(ProjectScope)
	if !armed || timeout != 30 {
		t.Fatalf("armed=%v timeout=%d, want true/30", armed, timeout)
	}
}

func TestRegisterBackfillHookRejectsUnrecognisable(t *testing.T) {
	withProjectDir(t)
	if err := RegisterBackfillHook(ProjectScope, "python3 something_else.py", 30); err == nil {
		t.Fatal("a command the arming check cannot find again must be refused")
	}
}

func TestRemoveBackfillPreservesUnrelatedStopHooks(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{
  "hooks": {
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "python3 /home/x/.claude/hooks/check-gate.py", "timeout": 320, "statusMessage": "running ./check.sh ..."},
        {"type": "command", "command": "steer commit-gate backfill --if-due --apply --quiet", "timeout": 30}
      ]}
    ]
  }
}`)
	if err := RemoveBackfillHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	hooks := stopHooks(t)
	if len(hooks) != 1 {
		t.Fatalf("one Stop hook must remain, got %d", len(hooks))
	}
	m := hooks[0].(map[string]any)
	if c := m["command"].(string); !contains(c, "check-gate.py") {
		t.Fatalf("the surviving hook must be check-gate, got %q", c)
	}
	// A Stop GROUP carries no matcher; register/remove must not add one. The regression
	// this guards against lands on the group object, not the inner command hook.
	if _, ok := stopGroup(t)["matcher"]; ok {
		t.Error("a Stop group must not gain a matcher key")
	}
	if _, ok := m["statusMessage"]; !ok {
		t.Error("the sibling's statusMessage must survive the round-trip")
	}
}

func TestRemoveBackfillDropsEmptyGroup(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{
  "hooks": {
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "steer commit-gate backfill --if-due --apply --quiet", "timeout": 30}
      ]}
    ]
  }
}`)
	if err := RemoveBackfillHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t)
	hooks, _ := settings["hooks"].(map[string]any)
	if stop, ok := hooks["Stop"]; ok {
		if arr, _ := stop.([]any); len(arr) > 0 {
			t.Fatalf("an emptied Stop group must be dropped, got %v", stop)
		}
	}
}

func TestBackfillRoundTripKeepsSiblingKeys(t *testing.T) {
	withProjectDir(t)
	// groupKey sits on the Stop GROUP (exercises StopHookEntry.extra); hookKey sits on
	// the CommandHook (exercises CommandHook.extra). Both must survive register+remove.
	original := `{
  "hooks": {
    "Stop": [
      {"groupKey": "group-survives", "hooks": [
        {"type": "command", "command": "python3 /home/x/.claude/hooks/check-gate.py", "timeout": 320, "statusMessage": "running ./check.sh ...", "hookKey": "keep-me"}
      ]}
    ]
  }
}`
	writeSettings(t, original)
	if err := RegisterBackfillHook(ProjectScope, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBackfillHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	group := stopGroup(t)
	if group["groupKey"] != "group-survives" {
		t.Errorf("an unmodeled Stop-group key must survive the round-trip, got %v", group["groupKey"])
	}
	hooks := stopHooks(t)
	if len(hooks) != 1 {
		t.Fatalf("the sibling hook must survive, got %d hooks", len(hooks))
	}
	m := hooks[0].(map[string]any)
	if m["hookKey"] != "keep-me" {
		t.Errorf("an unmodeled hook key must survive the round-trip, got %v", m["hookKey"])
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestTheResolverRidesStopAndSubagentStop pins that the resolver registers on both
// turn-end events, in a SubagentStop group of its own beside a user's matcher group,
// once however often it registers, and that both removal paths take it off both.
func TestTheResolverRidesStopAndSubagentStop(t *testing.T) {
	const resolver = "steer commit-gate backfill --if-due --apply --quiet"
	foreign := `{"hooks": {"SubagentStop": [
  {"matcher": "Explore", "hooks": [{"type": "command", "command": "~/.claude/hooks/sub.sh"}]}
]}}`
	subagentStop := func(t *testing.T) []any {
		hooks, _ := readSettings(t)["hooks"].(map[string]any)
		groups, _ := hooks["SubagentStop"].([]any)
		return groups
	}
	withProjectDir(t)
	writeSettings(t, foreign)
	for range 2 {
		if err := RegisterBackfillHook(ProjectScope, resolver, 30); err != nil {
			t.Fatal(err)
		}
	}
	if a := BackfillHookArming(ProjectScope); !a.Registered || !a.Complete || a.Timeout != 30 {
		t.Fatalf("arming = %+v, want registered on both events", a)
	}
	groups := subagentStop(t)
	if len(groups) != 2 {
		t.Fatalf("SubagentStop = %v, want the foreign group and one resolver group", groups)
	}
	if own := groups[1].(map[string]any); own["matcher"] != nil ||
		own["hooks"].([]any)[0].(map[string]any)["command"] != resolver {
		t.Fatalf("resolver group = %v, want the resolver alone with no matcher", own)
	}
	if err := RemoveBackfillHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if a := BackfillHookArming(ProjectScope); a.Registered {
		t.Fatalf("arming = %+v after removal", a)
	}
	if groups := subagentStop(t); len(groups) != 1 || groups[0].(map[string]any)["matcher"] != "Explore" {
		t.Fatalf("SubagentStop = %v, want the foreign group alone", groups)
	}

	writeSettings(t, `{"hooks": {"SubagentStop": [{"hooks": [{"type": "command", "command": "`+resolver+`"}]}]}}`)
	if a := BackfillHookArming(ProjectScope); !a.Registered || a.Complete {
		t.Fatalf("a SubagentStop-only resolver: arming = %+v, want registered and not complete", a)
	}
	writeSettings(t, `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "`+resolver+`"}]}],
  "SubagentStop": [{"matcher": "Explore", "hooks": [{"type": "command", "command": "`+resolver+`"}]}]}}`)
	if a := BackfillHookArming(ProjectScope); !a.Registered || a.Complete {
		t.Fatalf("a resolver limited to Explore agents: arming = %+v, want registered and not complete", a)
	}
	writeSettings(t, `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "`+resolver+`"}]}],
  "SubagentStop": [{"matcher": "Explore", "hooks": [{"type": "command", "command": "`+resolver+`"}]},
    {"hooks": [{"type": "command", "command": "`+resolver+`"}]}]}}`)
	if a := BackfillHookArming(ProjectScope); !a.Complete || len(a.Commands) != 3 {
		t.Fatalf("an every-agent resolver after a limited one: arming = %+v, want complete with three commands", a)
	}
	if removed, err := RemoveCommitGateAndResolver(ProjectScope); err != nil || !removed {
		t.Fatalf("RemoveCommitGateAndResolver = %v, %v; want true", removed, err)
	}
	if a := BackfillHookArming(ProjectScope); a.Registered {
		t.Fatalf("arming = %+v after RemoveCommitGateAndResolver, want no resolver", a)
	}
}
