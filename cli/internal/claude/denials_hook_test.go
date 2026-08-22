package claude

import (
	"testing"
)

func TestRegisterDenialsHookPreservesOtherPermissionDeniedHooks(t *testing.T) {
	withProjectTempDir(t)

	settingsPath := writeProjectSettings(t, `{
  "permissions": {"allow": ["Bash(ls:*)"]},
  "hooks": {
    "PermissionDenied": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "~/.claude/hooks/mine.sh"}]}
    ],
    "PreToolUse": [
      {"matcher": "Read", "hooks": [{"type": "command", "command": "~/.claude/hooks/other.sh"}]}
    ]
  }
}`)

	if err := RegisterDenialsHook(ProjectScope, "steer denials record", 10); err != nil {
		t.Fatalf("RegisterDenialsHook failed: %v", err)
	}

	raw := readSettingsRaw(t, settingsPath)
	if _, ok := raw["permissions"]; !ok {
		t.Error("unmodeled key permissions was dropped")
	}
	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is not an object: %#v", raw["hooks"])
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("PreToolUse was dropped by a PermissionDenied register")
	}
	entries, ok := hooks["PermissionDenied"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("PermissionDenied = %#v, want the foreign entry plus the recorder", hooks["PermissionDenied"])
	}
}

func TestRegisterDenialsHookIsIdempotent(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{}`)

	for i := 0; i < 3; i++ {
		if err := RegisterDenialsHook(ProjectScope, "steer denials record", 10); err != nil {
			t.Fatalf("RegisterDenialsHook failed: %v", err)
		}
	}
	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}
	if got := countDenialsHooks(settings.Hooks.PermissionDenied); got != 1 {
		t.Fatalf("recorder registered %d times, want 1", got)
	}
}

func TestRegisterDenialsHookRefusesAnUnrecognisableCommand(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{}`)

	if err := RegisterDenialsHook(ProjectScope, "steer report", 10); err == nil {
		t.Fatal("expected a refusal for a command that cannot be recognised again")
	}
}

func TestRemoveDenialsHookKeepsForeignHooks(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{
  "hooks": {
    "PermissionDenied": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "~/.claude/hooks/mine.sh"}]}
    ]
  }
}`)

	if err := RegisterDenialsHook(ProjectScope, "steer denials record", 10); err != nil {
		t.Fatalf("RegisterDenialsHook failed: %v", err)
	}
	if err := RemoveDenialsHook(ProjectScope); err != nil {
		t.Fatalf("RemoveDenialsHook failed: %v", err)
	}

	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}
	if countDenialsHooks(settings.Hooks.PermissionDenied) != 0 {
		t.Error("the recorder survived remove")
	}
	if len(settings.Hooks.PermissionDenied) != 1 {
		t.Fatalf("foreign PermissionDenied hook was dropped: %#v", settings.Hooks.PermissionDenied)
	}
}

func TestDenialsHookArmingReportsMatcherAndTimeout(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{}`)

	if err := RegisterDenialsHook(ProjectScope, "steer denials record", 10); err != nil {
		t.Fatalf("RegisterDenialsHook failed: %v", err)
	}
	arming, err := DenialsHookArming(ProjectScope)
	if err != nil {
		t.Fatalf("DenialsHookArming failed: %v", err)
	}
	if !arming.Registered || arming.Matcher != DenialsMatcher || arming.Timeout != 10 {
		t.Fatalf("arming = %+v", arming)
	}
}
