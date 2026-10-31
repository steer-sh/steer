package presets

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

type selfProtectionRuntime struct {
	root     string
	hookPath string
}

func setupSelfProtectionRuntime(t *testing.T) selfProtectionRuntime {
	t.Helper()

	root := t.TempDir()
	// The guard store is neutral; the harness registration it protects stays in
	// the harness's own config directory.
	claudeDir := filepath.Join(root, ".claude")
	hooksDir := filepath.Join(root, ".steer", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("failed to create claude dir: %v", err)
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
		{name: "register collision", command: "steer register .steer/hooks/sql-injection-prevention.py --rule \"x\""},
		{name: "demote collision", command: "steer register .steer/hooks/sql-injection-prevention.py --rule \"x\" --source manual"},
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
		{name: "register user hook", command: "steer register .steer/hooks/user-custom.py --rule \"x\""},
		{name: "preset list", command: "steer preset list"},
		{name: "preset enable", command: "steer preset enable sql-injection-prevention"},
		{name: "preset sync", command: "steer preset sync --dry-run"},
		{name: "team lock", command: "steer team lock"},
		{name: "team apply", command: "steer team apply --force"},
		{name: "team verify", command: "steer team verify --ci"},
		{name: "read lockfile", command: "cat steer-team.lock.json"},
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
		"rm .steer/hooks/steer-manifest.json",
		"rm .steer/hooks/steer-hooks.py",
		"echo '{}' > .claude/settings.json",
		"rm .steer/hooks/steer-audit.jsonl",
		"echo '' > .steer/hooks/steer-audit.jsonl",
		"rm steer-team.lock.json",
		"sed -i 's/factory/manual/' steer-team.lock.json",
		"echo '{}' > steer-team.lock.json",
		"rm .steer/hooks/steer-hooks.py; steer preset sync",
		"rm steer-team.lock.json && steer team apply",
		"echo done; rm .steer/hooks/steer-hooks.py",
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
				"file_path":  ".steer/hooks/steer-manifest.json",
				"new_string": "{}",
			},
		},
		{
			"tool_name": "Write",
			"tool_input": map[string]any{
				"file_path": ".steer/hooks/steer-hooks.py",
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
		{
			"tool_name": "Edit",
			"tool_input": map[string]any{
				"file_path":  "steer-team.lock.json",
				"new_string": "{}",
			},
		},
		{
			"tool_name": "Write",
			"tool_input": map[string]any{
				"file_path": ".steer/hooks/steer-audit.jsonl",
				"content":   "",
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

func TestSelfProtectionReadsEverySteerInvocationPastFlagsAndPunctuation(t *testing.T) {
	rt := setupSelfProtectionRuntime(t)

	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{"second invocation", "steer list && steer remove sql-injection-prevention", true},
		{"second invocation after a semicolon", "steer preset sync; steer preset disable sql-injection-prevention", true},
		{"invocation with no space before it", "steer list;steer remove sql-injection-prevention", true},
		{"invocation in a subshell", "(steer remove sql-injection-prevention)", true},
		{"path to the binary", "steer list && ./bin/steer remove sql-injection-prevention", true},
		{"root value flag before the subcommand", "steer --harness claude-code preset disable sql-injection-prevention", true},
		{"root scope flag before the subcommand", "steer --scope global remove sql-injection-prevention", true},
		{"root boolean flag before the subcommand", "steer --all-harnesses preset disable sql-injection-prevention", true},
		{"root flag after the group", "steer preset --harness codex disable sql-injection-prevention", true},
		{"remove value flag before the name", "steer remove --reason cleanup sql-injection-prevention", true},
		{"root value flag after remove", "steer remove --scope project sql-injection-prevention", true},
		{"attached flag value before the name", "steer remove --reason=cleanup sql-injection-prevention", true},
		{"register value flag before the path", "steer register --rule x .steer/hooks/sql-injection-prevention.py", true},
		{"quoted register value before the path", "steer register --rule \"a; b\" --source manual .steer/hooks/sql-injection-prevention.py", true},
		{"quoted value holding a separator", "steer remove --reason \";\" sql-injection-prevention", true},
		{"trailing semicolon on the name", "steer remove sql-injection-prevention; true", true},
		{"no space after the name", "steer remove sql-injection-prevention;true", true},
		{"trailing ampersands on the name", "steer remove sql-injection-prevention&& true", true},
		{"trailing semicolon on the preset action", "steer preset disable; true", true},
		{"redirection before the name", "steer remove 2>/dev/null sql-injection-prevention", true},
		{"redirection between a flag and its value", "steer remove --reason >/dev/null cleanup sql-injection-prevention", true},
		{"descriptor redirection between a flag and its value", "steer remove --reason 2>/dev/null cleanup sql-injection-prevention", true},
		{"redirection between a root flag and its value", "steer --harness 2>/dev/null codex preset disable sql-injection-prevention", true},
		{"quoted redirection as a flag value", "steer remove --reason \">\" sql-injection-prevention", true},
		{"numeric flag value before a redirection", "steer remove --reason 2 >/dev/null sql-injection-prevention", true},
		{"redirection between a flag and its value for a user hook", "steer remove --reason >/dev/null cleanup user-custom", false},
		{"unbalanced quote later in the command", "steer remove sql-injection-prevention; echo \"", true},
		{"list then a user hook", "steer list && steer remove user-custom", false},
		{"sync then list", "steer preset sync && steer list", false},
		{"sync with a root flag", "steer --harness codex preset sync", false},
		{"team commands chained", "steer team apply --force && steer team verify --ci", false},
		{"user hook with a reason naming a factory hook", "steer remove --reason sql-injection-prevention user-custom", false},
		{"user hook with its redirection", "steer remove user-custom 2>&1 && steer list", false},
		{"user register with a rule naming a removal", "steer register --rule \"steer remove sql-injection-prevention\" .steer/hooks/user-custom.py", false},
		{"directory named steer", "cd steer && steer list", false},
		{"factory name in the next command", "steer remove --force; echo sql-injection-prevention", false},
		{"disable word in the next command", "steer preset --help && echo disable", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason, output, err := runSelfProtectionHook(t, rt, bashPayload(tc.command))
			if err != nil {
				t.Fatalf("hook execution failed: %v output=%s", err, output)
			}
			if blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v, reason=%q output=%s", blocked, tc.blocked, reason, output)
			}
		})
	}
}

func TestSelfProtectionInstallsItsCommandReadingAtNewVersionIntoMaster(t *testing.T) {
	dir := withPresetProjectTempDir(t)
	testhome.Set(t, dir)
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	item, ok := catalog.Find("sql-injection-prevention")
	if !ok {
		t.Fatal("sql-injection-prevention missing from catalog")
	}
	if _, err := EnablePreset(claude.ProjectScope, catalog, item, false); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAutoManagedFactoryPresets(claude.ProjectScope, catalog, false); err != nil {
		t.Fatal(err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Hooks["steer-self-protection"].HookVersion; got != "factory-hook-v3" {
		t.Fatalf("manifest hook version = %q, want factory-hook-v3", got)
	}

	master := filepath.Join(dir, ".steer", "hooks", "steer-hooks.py")
	cases := []struct {
		command string
		blocked bool
	}{
		{"steer list && steer remove --reason cleanup sql-injection-prevention; true", true},
		{"steer --harness claude-code preset disable sql-injection-prevention", true},
		{"steer list && steer preset sync", false},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			if got := runGuardPayload(t, master, dir, "Bash", map[string]any{"command": tc.command}); got != tc.blocked {
				t.Fatalf("blocked = %v, want %v", got, tc.blocked)
			}
		})
	}
}
