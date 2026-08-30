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

func TestRegisterBackfillReplacesPythonResolver(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{
  "hooks": {
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "python3 /home/x/.claude/hooks/read-provenance.py", "timeout": 20},
        {"type": "command", "command": "python3 /home/x/.claude/hooks/commit-review/backfill_ledger.py --if-due --apply --quiet", "timeout": 30}
      ]}
    ]
  }
}`)
	if err := RegisterBackfillHook(ProjectScope, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	hooks := stopHooks(t)
	var commands []string
	for _, h := range hooks {
		m := h.(map[string]any)
		commands = append(commands, m["command"].(string))
	}
	sawPython, sawGo, sawSibling := false, false, false
	for _, c := range commands {
		switch {
		case contains(c, "backfill_ledger.py"):
			sawPython = true
		case contains(c, "commit-gate backfill"):
			sawGo = true
		case contains(c, "read-provenance.py"):
			sawSibling = true
		}
	}
	if sawPython {
		t.Error("the Python resolver entry must be replaced")
	}
	if !sawGo {
		t.Error("the Go resolver entry must be present")
	}
	if !sawSibling {
		t.Error("the unrelated read-provenance Stop hook must survive")
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
