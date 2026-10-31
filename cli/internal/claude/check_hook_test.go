package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckHookSitsBesideTheResolverAndForeignStopHooks(t *testing.T) {
	withProjectTempDir(t)
	settingsPath := writeProjectSettings(t, `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "~/.claude/hooks/mine.sh"}]}
    ],
    "SubagentStop": [
      {"hooks": [{"type": "command", "command": "~/.claude/hooks/sub.sh"}]}
    ]
  }
}`)
	if err := RegisterBackfillHook(ProjectScope, "steer commit-gate backfill --if-due", 30); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := RegisterCheckHook(ProjectScope, "steer check hook --harness claude-code", 330); err != nil {
			t.Fatal(err)
		}
	}

	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	runners, commands := 0, map[string]bool{}
	for _, group := range settings.Hooks.Stop {
		for _, hook := range group.Hooks {
			commands[hook.Command] = true
			if IsCheckHookCommand(hook.Command) {
				runners++
				if hook.Timeout != 330 || hook.StatusMessage != CheckHookStatusMessage {
					t.Fatalf("runner hook = %+v, want timeout 330 and the status message", hook)
				}
			}
		}
	}
	if runners != 1 {
		t.Fatalf("runner registered %d times, want 1", runners)
	}
	if !commands["~/.claude/hooks/mine.sh"] || !BackfillHookArming(ProjectScope).Registered {
		t.Fatalf("a foreign Stop hook or the resolver was lost: %v", commands)
	}
	hooks := readSettingsRaw(t, settingsPath)["hooks"].(map[string]any)
	if sub := hooks["SubagentStop"].([]any); len(sub) != 1 {
		t.Fatalf("SubagentStop = %v, want only the foreign entry", sub)
	}

	removed, err := RemoveCheckHook(ProjectScope)
	if err != nil || !removed {
		t.Fatalf("RemoveCheckHook = %v, %v; want true", removed, err)
	}
	if a, _ := CheckHookArming(ProjectScope); a.Registered {
		t.Fatal("the runner is still registered after removal")
	}
	if !BackfillHookArming(ProjectScope).Registered {
		t.Fatal("removing the runner removed the resolver")
	}
	if removed, err := RemoveCheckHook(ProjectScope); err != nil || removed {
		t.Fatalf("a second RemoveCheckHook = %v, %v; want false", removed, err)
	}
}

func TestCommitGateWritesKeepTheCheckHook(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{}`)
	if err := RegisterCheckHook(ProjectScope, "steer check hook --harness claude-code", 330); err != nil {
		t.Fatal(err)
	}

	if err := RegisterCommitGateAndResolver(ProjectScope, "steer commit-gate review", 240,
		"steer commit-gate backfill", 30); err != nil {
		t.Fatal(err)
	}
	if a, _ := CheckHookArming(ProjectScope); !a.Registered {
		t.Fatal("installing the commit gate removed the check runner")
	}
	if _, err := RemoveCommitGateAndResolver(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if a, _ := CheckHookArming(ProjectScope); !a.Registered || a.Timeout != 330 {
		t.Fatalf("removing the commit gate changed the check runner: %+v", a)
	}
}

func TestRegisterCheckHookRefusesAnUnrecognisableCommand(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{}`)

	if err := RegisterCheckHook(ProjectScope, "bash check.sh", 330); err == nil {
		t.Fatal("expected a refusal for a command that cannot be recognised again")
	}
}

// TestRemoveCommitGateReportsAndSkipsTheWriteWhenAbsent: a removal with nothing to
// remove reports false and leaves the file alone.
func TestRemoveCommitGateReportsAndSkipsTheWriteWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const text = "{\n   \"hooks\": {}\n}\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveCommitGateAndResolver(ProjectScope)
	if err != nil || removed {
		t.Fatalf("removed, err = %v, %v; want false, nil", removed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != text {
		t.Errorf("settings rewritten to %q", got)
	}
	if err := RegisterCommitGateAndResolver(ProjectScope, "steer commit-gate review", 460, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveCommitGateAndResolver(ProjectScope); err != nil || !removed {
		t.Fatalf("removed, err = %v, %v; want true, nil", removed, err)
	}
}
