package presets

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type selfProtectionRuntime struct {
	root     string
	hookPath string
}

func setupSelfProtectionRuntime(t *testing.T) selfProtectionRuntime {
	t.Helper()

	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	hooksDir := filepath.Join(claudeDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}

	scriptSource := filepath.Join("hooks", "steer-self-protection.py")
	scriptData, err := os.ReadFile(scriptSource)
	if err != nil {
		t.Fatalf("failed to read self-protection hook source: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "steer-self-protection.py")
	if err := os.WriteFile(hookPath, scriptData, 0o755); err != nil {
		t.Fatalf("failed to write self-protection hook runtime file: %v", err)
	}

	for _, file := range []string{"sql-injection-prevention.py", "user-custom.py", "steer-hooks.py"} {
		path := filepath.Join(hooksDir, file)
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n\ndef main():\n    return\n"), 0o755); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	manifest := map[string]any{
		"hooks": map[string]any{
			"steer-self-protection": map[string]any{
				"file":      "steer-self-protection.py",
				"rule":      "self protection",
				"source":    "factory",
				"source_id": "steer-self-protection",
			},
			"sql-injection-prevention": map[string]any{
				"file":      "sql-injection-prevention.py",
				"rule":      "sql safety",
				"source":    "factory",
				"source_id": "sql-injection-prevention",
			},
			"user-custom": map[string]any{
				"file":      "user-custom.py",
				"rule":      "user rule",
				"source":    "manual",
				"source_id": "user-custom",
			},
		},
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal runtime manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "steer-manifest.json"), manifestData, 0o644); err != nil {
		t.Fatalf("failed to write runtime manifest: %v", err)
	}

	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("failed to write runtime settings.json: %v", err)
	}

	return selfProtectionRuntime{
		root:     root,
		hookPath: hookPath,
	}
}

func runSelfProtectionHook(t *testing.T, rt selfProtectionRuntime, payload map[string]any) (blocked bool, reason string, output string, err error) {
	t.Helper()

	input, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		t.Fatalf("failed to marshal payload: %v", marshalErr)
	}

	cmd := exec.Command("python3", rt.hookPath)
	cmd.Dir = rt.root
	cmd.Stdin = bytes.NewReader(input)
	raw, err := cmd.CombinedOutput()
	output = strings.TrimSpace(string(raw))

	if output == "" {
		return false, "", output, err
	}

	var parsed map[string]any
	if unmarshalErr := json.Unmarshal(raw, &parsed); unmarshalErr == nil {
		decision, _ := parsed["decision"].(string)
		reason, _ = parsed["stopReason"].(string)
		return decision == "block", reason, output, err
	}

	if strings.Contains(strings.Join(strings.Fields(output), ""), `"decision":"block"`) {
		return true, "", output, err
	}
	return false, "", output, err
}

func bashPayload(command string) map[string]any {
	return map[string]any{
		"tool_name": "Bash",
		"tool_input": map[string]any{
			"command": command,
		},
	}
}

func TestSelfProtectionBlocksFactoryMutations(t *testing.T) {
	rt := setupSelfProtectionRuntime(t)

	tests := []struct {
		name    string
		command string
	}{
		{name: "remove factory hook", command: "steer remove sql-injection-prevention"},
		{name: "disable factory preset", command: "steer preset disable sql-injection-prevention"},
		{name: "register collision", command: "steer register .claude/hooks/sql-injection-prevention.py --rule \"x\""},
		{name: "demote collision", command: "steer register .claude/hooks/sql-injection-prevention.py --rule \"x\" --source manual"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason, output, err := runSelfProtectionHook(t, rt, bashPayload(tc.command))
			if err != nil {
				t.Fatalf("hook execution failed: %v output=%s", err, output)
			}
			if !blocked {
				t.Fatalf("expected command to be blocked, reason=%q output=%s", reason, output)
			}
		})
	}
}

func TestSelfProtectionAllowsUserMutationsAndReadOnlyOps(t *testing.T) {
	rt := setupSelfProtectionRuntime(t)

	tests := []struct {
		name    string
		command string
	}{
		{name: "remove user hook", command: "steer remove user-custom"},
		{name: "register user hook", command: "steer register .claude/hooks/user-custom.py --rule \"x\""},
		{name: "preset list", command: "steer preset list"},
		{name: "preset enable", command: "steer preset enable sql-injection-prevention"},
		{name: "preset sync", command: "steer preset sync --dry-run"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason, output, err := runSelfProtectionHook(t, rt, bashPayload(tc.command))
			if err != nil {
				t.Fatalf("hook execution failed: %v output=%s", err, output)
			}
			if blocked {
				t.Fatalf("expected command to be allowed, reason=%q output=%s", reason, output)
			}
		})
	}
}

func TestSelfProtectionBlocksProtectedFileMutationsViaBash(t *testing.T) {
	rt := setupSelfProtectionRuntime(t)

	tests := []string{
		"rm .claude/hooks/steer-manifest.json",
		"rm .claude/hooks/steer-hooks.py",
		"echo '{}' > .claude/settings.json",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			blocked, reason, output, err := runSelfProtectionHook(t, rt, bashPayload(command))
			if err != nil {
				t.Fatalf("hook execution failed: %v output=%s", err, output)
			}
			if !blocked {
				t.Fatalf("expected protected file mutation to be blocked, reason=%q output=%s", reason, output)
			}
		})
	}
}

func TestSelfProtectionBlocksProtectedFileMutationsViaEditWriteMultiEdit(t *testing.T) {
	rt := setupSelfProtectionRuntime(t)

	tests := []map[string]any{
		{
			"tool_name": "Edit",
			"tool_input": map[string]any{
				"file_path":  ".claude/hooks/steer-manifest.json",
				"new_string": "{}",
			},
		},
		{
			"tool_name": "Write",
			"tool_input": map[string]any{
				"file_path": ".claude/hooks/steer-hooks.py",
				"content":   "print('x')",
			},
		},
		{
			"tool_name": "MultiEdit",
			"tool_input": map[string]any{
				"file_path": ".claude/settings.json",
				"edits": []map[string]any{
					{"old_string": "{}", "new_string": "{\"hooks\": {}}"},
				},
			},
		},
	}

	for idx, payload := range tests {
		t.Run(string(rune('A'+idx)), func(t *testing.T) {
			blocked, reason, output, err := runSelfProtectionHook(t, rt, payload)
			if err != nil {
				t.Fatalf("hook execution failed: %v output=%s", err, output)
			}
			if !blocked {
				t.Fatalf("expected protected file mutation to be blocked, reason=%q output=%s", reason, output)
			}
		})
	}
}
