package claude

import (
	"testing"
)

// A user's own ~/.claude/statusline.sh is not steer's. Reading it as steer's would
// let install overwrite it without asking, and let remove delete it.
func TestStatusLineIdentityRejectsAForeignScript(t *testing.T) {
	foreign := []string{
		"~/.claude/statusline.sh",
		"ccstatusline",
		"whoami",
		"~/bin/steer-statusline.sh",
		// The head has to BE steer, not merely mention it.
		"statusline --config ~/.steer/config.json",
		"ccstatusline --theme steer",
	}
	for _, command := range foreign {
		if IsStatusLineCommand(command) {
			t.Errorf("IsStatusLineCommand(%q) = true, want false", command)
		}
	}
	for _, command := range []string{
		"steer statusline",
		"/Users/x/.steer/bin/steer statusline",
		"steer statusline --wrap-b64 d2hvYW1p",
	} {
		if !IsStatusLineCommand(command) {
			t.Errorf("IsStatusLineCommand(%q) = false, want true", command)
		}
	}
}

// remove has to recover the wrapped command byte for byte, so the round trip is
// tested over the forms a shell-quoted value would lose.
func TestWrappedCommandRoundTrips(t *testing.T) {
	for _, original := range []string{
		"whoami",
		`~/.claude/statusline.sh`,
		`jq -r '"[\(.model.display_name)] \(.context_window.used_percentage // 0)%"'`,
		`sh -c "echo 'it'\''s here'"`,
	} {
		command := "steer statusline " + EncodeWrapped(original)
		got, ok := WrappedCommand(command)
		if !ok {
			t.Fatalf("WrappedCommand(%q) reported no wrapped command", command)
		}
		if got != original {
			t.Errorf("WrappedCommand() = %q, want %q", got, original)
		}
	}
}

func TestWrappedCommandReportsAnUnwrappedEntry(t *testing.T) {
	for _, command := range []string{"steer statusline", "steer statusline --wrap-b64 ", "steer statusline --wrap-b64 !!!"} {
		if _, ok := WrappedCommand(command); ok {
			t.Errorf("WrappedCommand(%q) reported a wrapped command", command)
		}
	}
}

// padding and refreshInterval are the operator's, not the command's.
func TestRegisterStatusLineKeepsTheEntrysOtherKeys(t *testing.T) {
	withProjectTempDir(t)
	settingsPath := writeProjectSettings(t, `{
  "statusLine": {"type": "command", "command": "whoami", "padding": 2, "refreshInterval": 5},
  "hooks": {}
}`)

	if err := RegisterStatusLine(ProjectScope, "steer statusline"); err != nil {
		t.Fatalf("RegisterStatusLine failed: %v", err)
	}

	raw := readSettingsRaw(t, settingsPath)
	entry, ok := raw["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing or wrong shape: %v", raw["statusLine"])
	}
	if entry["command"] != "steer statusline" {
		t.Errorf("command = %v, want steer statusline", entry["command"])
	}
	for _, key := range []string{"padding", "refreshInterval"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("key %q was dropped by the register round-trip", key)
		}
	}
}

func TestRemoveStatusLineRestoresTheWrappedCommand(t *testing.T) {
	withProjectTempDir(t)
	original := `jq -r '"[\(.model.display_name)]"'`
	settingsPath := writeProjectSettings(t, `{
  "statusLine": {"type": "command", "command": "steer statusline `+EncodeWrapped(original)+`", "padding": 2},
  "hooks": {}
}`)

	restored, err := RemoveStatusLine(ProjectScope)
	if err != nil {
		t.Fatalf("RemoveStatusLine failed: %v", err)
	}
	if restored != original {
		t.Errorf("restored = %q, want %q", restored, original)
	}

	raw := readSettingsRaw(t, settingsPath)
	entry, ok := raw["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine was cleared, want the wrapped command restored in place")
	}
	if entry["command"] != original {
		t.Errorf("command = %v, want %q", entry["command"], original)
	}
	if _, ok := entry["padding"]; !ok {
		t.Errorf("padding was dropped by the remove round-trip")
	}
}

func TestRemoveStatusLineClearsAnUnwrappedEntry(t *testing.T) {
	withProjectTempDir(t)
	settingsPath := writeProjectSettings(t, `{
  "statusLine": {"type": "command", "command": "steer statusline"},
  "hooks": {}
}`)

	if _, err := RemoveStatusLine(ProjectScope); err != nil {
		t.Fatalf("RemoveStatusLine failed: %v", err)
	}
	raw := readSettingsRaw(t, settingsPath)
	if _, ok := raw["statusLine"]; ok {
		t.Errorf("statusLine survived the remove: %v", raw["statusLine"])
	}
}

func TestRemoveStatusLineLeavesAForeignEntryAlone(t *testing.T) {
	withProjectTempDir(t)
	settingsPath := writeProjectSettings(t, `{
  "statusLine": {"type": "command", "command": "~/.claude/statusline.sh"},
  "hooks": {}
}`)

	if _, err := RemoveStatusLine(ProjectScope); err == nil {
		t.Fatalf("RemoveStatusLine removed a status line steer did not install")
	}
	raw := readSettingsRaw(t, settingsPath)
	entry, ok := raw["statusLine"].(map[string]any)
	if !ok || entry["command"] != "~/.claude/statusline.sh" {
		t.Errorf("the foreign status line was changed: %v", raw["statusLine"])
	}
}

func TestRegisterStatusLineRefusesAnUnrecognisableCommand(t *testing.T) {
	withProjectTempDir(t)
	writeProjectSettings(t, `{"hooks": {}}`)

	if err := RegisterStatusLine(ProjectScope, "whoami"); err == nil {
		t.Fatalf("RegisterStatusLine accepted a command it could not recognise again")
	}
}
