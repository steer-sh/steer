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

// A rewrite of the global settings file keeps the earlier bytes beside it; a
// project's file gets no copy, which would sit untracked in the repository.
func TestSaveSettingsBacksUpOnlyTheGlobalFile(t *testing.T) {
	withProjectTempDir(t)
	spacedHome(t)
	for _, scope := range []string{ProjectScope, GlobalScope} {
		if err := SaveSettings(scope, &Settings{}); err != nil {
			t.Fatal(err)
		}
		if err := SaveSettings(scope, &Settings{}); err != nil {
			t.Fatal(err)
		}
	}
	project, _ := GetSettingsFile(ProjectScope)
	if _, err := os.Stat(project + BackupExtension); !os.IsNotExist(err) {
		t.Fatalf("project save left %s", project+BackupExtension)
	}
	global, _ := GetSettingsFile(GlobalScope)
	if _, err := os.Stat(global + BackupExtension); err != nil {
		t.Fatalf("global save left no backup: %v", err)
	}
}

// A project settings file is written only where it resolves inside the
// repository: a linked .claude directory or settings file that leads outside
// is refused and its target left as it was, and a link inside is kept.
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

// settingsOnlyWrites names the settings writes that write no store and register
// no guard master.
func settingsOnlyWrites() map[string]func() error {
	const (
		review   = "steer commit-gate review"
		resolver = "steer commit-gate backfill"
	)
	return map[string]func() error{
		"SaveSettings": func() error { return SaveSettings(ProjectScope, &Settings{}) },
		"RegisterCommitGateHook": func() error {
			return RegisterCommitGateHook(ProjectScope, review, 210)
		},
		"RegisterCommitGateAndResolver": func() error {
			return RegisterCommitGateAndResolver(ProjectScope, review, 210, resolver, 30)
		},
		"RegisterBackfillHook": func() error {
			return RegisterBackfillHook(ProjectScope, resolver, 30)
		},
		"RegisterCheckHook": func() error {
			return RegisterCheckHook(ProjectScope, "steer check hook --harness claude-code", 30)
		},
		"RegisterStatusLine": func() error {
			return RegisterStatusLine(ProjectScope, "steer statusline")
		},
		"RemoveMasterHook": func() error {
			settings := &Settings{Hooks: Hooks{PreToolUse: []HookEntry{{
				Matcher: ".*",
				Hooks:   []CommandHook{{Type: "command", Command: "python3 .steer/hooks/" + MasterHookName}},
			}}}}
			if err := SaveSettings(ProjectScope, settings); err != nil {
				return err
			}
			return RemoveMasterHook(ProjectScope)
		},
	}
}

// A settings write in a repository with no guards leaves no guard store behind.
func TestSettingsWritesCreateNoGuardStore(t *testing.T) {
	for name, write := range settingsOnlyWrites() {
		t.Run(name, func(t *testing.T) {
			withProjectTempDir(t)
			if err := write(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(ClaudeDirName, SettingsName)); err != nil {
				t.Fatalf("the settings file was not written: %v", err)
			}
			if _, err := os.Lstat(".steer"); !os.IsNotExist(err) {
				t.Fatalf(".steer exists after a settings write: %v", err)
			}
		})
	}
}

// A link at the store's .gitignore refuses store writes and never a settings
// write, which reads and writes nothing through it.
func TestSettingsWritesIgnoreALinkedStoreIgnoreFile(t *testing.T) {
	for name, write := range settingsOnlyWrites() {
		t.Run(name, func(t *testing.T) {
			withProjectTempDir(t)
			store := filepath.Join(".steer", "hooks")
			if err := os.MkdirAll(store, 0o755); err != nil {
				t.Fatal(err)
			}
			const kept = "private\n"
			target := filepath.Join(t.TempDir(), "private")
			if err := os.WriteFile(target, []byte(kept), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(store, ".gitignore")); err != nil {
				t.Fatal(err)
			}
			if err := write(); err != nil {
				t.Fatal(err)
			}
			if raw, _ := os.ReadFile(target); string(raw) != kept {
				t.Fatalf("the link's target changed: %q", raw)
			}
		})
	}
}

// Registering the guard master still prepares the store it names and refuses a
// linked one.
//
// pins existing behaviour
func TestRegisterMasterHookPreparesTheStore(t *testing.T) {
	withProjectTempDir(t)
	if err := RegisterMasterHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(".steer", "hooks", ".gitignore")); err != nil {
		t.Fatalf("the store was not prepared: %v", err)
	}

	withProjectTempDir(t)
	if err := os.Symlink(t.TempDir(), ".steer"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMasterHook(ProjectScope); err == nil {
		t.Fatal("RegisterMasterHook accepted a linked .steer")
	}
	if _, err := os.Stat(filepath.Join(ClaudeDirName, SettingsName)); !os.IsNotExist(err) {
		t.Fatalf("the settings file was written after the refusal: %v", err)
	}
}
