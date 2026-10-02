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

// A project settings file is written only where it resolves inside the
// repository: a linked .claude directory, settings file or backup that leads
// outside is refused and its target left as it was, and a link inside is kept.
func TestSaveSettingsBoundsAProjectLinkByTheRepository(t *testing.T) {
	const kept = "kept\n"
	setup := map[string]func(t *testing.T, outside string){
		"linked .claude": func(t *testing.T, outside string) {
			if err := os.Symlink(outside, ClaudeDirName); err != nil {
				t.Fatal(err)
			}
		},
		"linked settings.json": func(t *testing.T, outside string) {
			if err := os.Mkdir(ClaudeDirName, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, SettingsName), filepath.Join(ClaudeDirName, SettingsName)); err != nil {
				t.Fatal(err)
			}
		},
		"linked backup": func(t *testing.T, outside string) {
			if err := os.Mkdir(ClaudeDirName, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ClaudeDirName, SettingsName), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, SettingsName), filepath.Join(ClaudeDirName, SettingsName+BackupExtension)); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, link := range setup {
		t.Run(name, func(t *testing.T) {
			withProjectTempDir(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, SettingsName), []byte(kept), 0o644); err != nil {
				t.Fatal(err)
			}
			link(t, outside)

			if err := SaveSettings(ProjectScope, &Settings{}); err == nil {
				t.Fatal("SaveSettings wrote through a link that leaves the repository")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("SaveSettings left %d entries at the link's target", len(entries))
			}
			if raw, _ := os.ReadFile(filepath.Join(outside, SettingsName)); string(raw) != kept {
				t.Fatalf("the outside file changed: %q", raw)
			}
		})
	}

	t.Run("link inside the repository", func(t *testing.T) {
		withProjectTempDir(t)
		if err := os.WriteFile("shared-settings.json", []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(ClaudeDirName, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(ClaudeDirName, SettingsName)
		if err := os.Symlink(filepath.Join("..", "shared-settings.json"), link); err != nil {
			t.Fatal(err)
		}

		if err := SaveSettings(ProjectScope, &Settings{}); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the link was replaced: %v, %v", info, err)
		}
		if raw, _ := os.ReadFile("shared-settings.json"); string(raw) == "{}\n" {
			t.Fatal("the settings were not written at the link's target")
		}
	})
}
