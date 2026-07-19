package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeProjectSettings(t *testing.T, content string) string {
	t.Helper()

	if err := os.MkdirAll(ClaudeDirName, 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}
	settingsPath := filepath.Join(ClaudeDirName, SettingsName)
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}
	return settingsPath
}

func readSettingsRaw(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}
	return raw
}

func TestRegisterMasterHookPreservesUnmodeledKeys(t *testing.T) {
	withProjectTempDir(t)

	settingsPath := writeProjectSettings(t, `{
  "permissions": {"allow": ["Bash(ls:*)"]},
  "env": {"FOO": "bar"},
  "model": "opus",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "~/.claude/hooks/notes-index.sh"}]}
    ],
    "PreToolUse": [
      {"matcher": "Read", "hooks": [{"type": "command", "command": "~/.claude/hooks/other.sh"}]}
    ]
  }
}`)

	if err := RegisterMasterHook(ProjectScope); err != nil {
		t.Fatalf("RegisterMasterHook failed: %v", err)
	}

	raw := readSettingsRaw(t, settingsPath)
	for _, key := range []string{"permissions", "env", "model"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("top-level key %q was dropped by register round-trip", key)
		}
	}

	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or wrong shape: %v", raw["hooks"])
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Errorf("hooks.SessionStart was dropped by register round-trip")
	}

	pre, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("hooks.PreToolUse missing or wrong shape: %v", hooks["PreToolUse"])
	}
	if len(pre) != 2 {
		t.Fatalf("expected 2 PreToolUse entries (preserved + steer), got %d", len(pre))
	}

	data, _ := json.Marshal(raw)
	if !json.Valid(data) {
		t.Fatalf("round-tripped settings are not valid JSON")
	}
}

func TestRemoveMasterHookPreservesUnmodeledKeys(t *testing.T) {
	withProjectTempDir(t)

	settingsPath := writeProjectSettings(t, `{
  "statusLine": {"type": "command", "command": "whoami"},
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "~/.claude/hooks/notes-index.sh"}]}
    ],
    "PreToolUse": [
      {"matcher": ".*", "hooks": [{"type": "command", "command": "python3 .claude/hooks/steer-hooks.py"}]}
    ]
  }
}`)

	if err := RemoveMasterHook(ProjectScope); err != nil {
		t.Fatalf("RemoveMasterHook failed: %v", err)
	}

	raw := readSettingsRaw(t, settingsPath)
	if _, ok := raw["statusLine"]; !ok {
		t.Errorf("top-level key statusLine was dropped by remove round-trip")
	}

	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or wrong shape: %v", raw["hooks"])
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Errorf("hooks.SessionStart was dropped by remove round-trip")
	}
	if _, ok := hooks["PreToolUse"]; ok {
		t.Errorf("steer-only PreToolUse should have been removed entirely, got %v", hooks["PreToolUse"])
	}
}

func TestLoadSettingsMissingFileReturnsEmpty(t *testing.T) {
	withProjectTempDir(t)

	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatalf("LoadSettings on missing file failed: %v", err)
	}
	if len(settings.Hooks.PreToolUse) != 0 {
		t.Fatalf("expected empty PreToolUse, got %v", settings.Hooks.PreToolUse)
	}
	if err := SaveSettings(ProjectScope, settings); err != nil {
		t.Fatalf("SaveSettings of empty settings failed: %v", err)
	}
}
