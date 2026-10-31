package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readHooks(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	return hooks
}

func TestCheckHookRegistersOnRootStopOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other","futureKey":true}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterCommitGateAndResolver("global", "", "steer commit-gate review-codex", 240,
		"steer commit-gate backfill", 30); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := RegisterCheckHook("global", "", "steer check hook --harness codex", 330); err != nil {
			t.Fatal(err)
		}
	}

	a := CheckHookArming("global", "")
	if !a.Registered || a.Timeout != 330 || a.Path != path {
		t.Fatalf("arming = %+v, want registered with timeout 330 at %s", a, path)
	}
	hooks := readHooks(t, path)
	stop := hooks["Stop"].([]any)
	if len(stop) != 3 {
		t.Fatalf("Stop entries = %d, want the foreign one, the resolver and the runner: %v", len(stop), stop)
	}
	for _, raw := range hooks["SubagentStop"].([]any) {
		for _, hook := range raw.(map[string]any)["hooks"].([]any) {
			if isCheckHook(hook.(map[string]any)["command"].(string)) {
				t.Fatal("the runner was registered on SubagentStop")
			}
		}
	}
	if foreign := stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any); foreign["futureKey"] != true {
		t.Fatalf("an unrelated handler lost a key: %v", foreign)
	}

	if _, removed, err := RemoveCheckHook("global", ""); err != nil || !removed {
		t.Fatalf("RemoveCheckHook = %v, %v; want removed", removed, err)
	}
	if CheckHookArming("global", "").Registered {
		t.Fatal("the runner is still registered after removal")
	}
	if registered, _ := ResolverArming("global", ""); !registered {
		t.Fatal("removing the runner removed the resolver")
	}
	if _, removed, err := RemoveCheckHook("global", ""); err != nil || removed {
		t.Fatalf("a second RemoveCheckHook = %v, %v; want nothing removed", removed, err)
	}
}

func TestCommitGateWritesKeepTheCheckHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := RegisterCheckHook("global", "", "steer check hook --harness codex", 330); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterCommitGateAndResolver("global", "", "steer commit-gate review-codex", 240,
		"steer commit-gate backfill", 30); err != nil {
		t.Fatal(err)
	}
	if !CheckHookArming("global", "").Registered {
		t.Fatal("installing the commit gate removed the check runner")
	}
	if _, err := RemoveCommitGateAndResolver("global", ""); err != nil {
		t.Fatal(err)
	}
	if a := CheckHookArming("global", ""); !a.Registered || a.Timeout != 330 {
		t.Fatalf("removing the commit gate changed the check runner: %+v", a)
	}
}

func TestRemovingTheOnlyCheckHookDropsTheStopKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := RegisterCheckHook("global", "", "steer check hook --harness codex", 330); err != nil {
		t.Fatal(err)
	}

	if _, _, err := RemoveCheckHook("global", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHooks(t, filepath.Join(home, "hooks.json"))["Stop"]; ok {
		t.Fatal("an empty Stop list was left behind")
	}
}
