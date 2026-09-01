package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hooksFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

// commandsUnder lists every PreToolUse command, paired with its entry's matcher.
func commandsUnder(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := hooksFile(t, path)
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["PreToolUse"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		matcher, _ := entry["matcher"].(string)
		list, _ := entry["hooks"].([]any)
		for _, rawHook := range list {
			hook, _ := rawHook.(map[string]any)
			command, _ := hook["command"].(string)
			out[command] = matcher
		}
	}
	return out
}

func TestMasterRegistersUnderBothToolPaths(t *testing.T) {
	dir := t.TempDir()
	path, err := RegisterMaster("project", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".codex", "hooks.json"); path != want {
		t.Errorf("path = %q; want %q", path, want)
	}

	found := ""
	for command, matcher := range commandsUnder(t, path) {
		if !isMaster(command) {
			continue
		}
		found = command
		if matcher != MasterMatcher {
			t.Errorf("matcher = %q; want %q, or a guard never sees file edits", matcher, MasterMatcher)
		}
	}
	if found == "" {
		t.Fatal("the master was not registered")
	}
	// The one master file serves both harnesses and reads its reply contract
	// off this flag, so a Codex registration that omits it answers in Claude's.
	if !strings.Contains(found, "--harness codex") {
		t.Errorf("command = %q; want the codex harness flag", found)
	}
}

func TestMasterAndCommitGateAreIndependentChannels(t *testing.T) {
	dir := t.TempDir()
	if _, err := RegisterCommitGate("project", dir, "steer commit-gate review-codex", 90); err != nil {
		t.Fatal(err)
	}
	path, err := RegisterMaster("project", dir)
	if err != nil {
		t.Fatal(err)
	}

	commands := commandsUnder(t, path)
	gate, master := 0, 0
	for command := range commands {
		if isGate(command) {
			gate++
		}
		if isMaster(command) {
			master++
		}
	}
	if gate != 1 || master != 1 {
		t.Fatalf("gate=%d master=%d; installing one channel disturbed the other", gate, master)
	}

	// Removing the guard master leaves the gate armed, and the reverse.
	if _, err := RemoveMaster("project", dir); err != nil {
		t.Fatal(err)
	}
	commands = commandsUnder(t, path)
	for command := range commands {
		if isMaster(command) {
			t.Error("RemoveMaster left the master registered")
		}
	}
	if len(commands) != 1 {
		t.Errorf("commands = %v; RemoveMaster took the gate out too", commands)
	}
}

func TestMasterRegistrationLeavesUnrelatedHooksAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const seed = `{
  "unrelatedTopLevel": {"keep": true},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "someone-elses-hook"}]}
    ],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "another-tool"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterMaster("project", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveMaster("project", dir); err != nil {
		t.Fatal(err)
	}

	root := hooksFile(t, path)
	if _, ok := root["unrelatedTopLevel"]; !ok {
		t.Error("an unrelated top-level key was dropped")
	}
	hooks, _ := root["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("an unrelated event was dropped")
	}
	commands := commandsUnder(t, path)
	if _, ok := commands["someone-elses-hook"]; !ok {
		t.Errorf("commands = %v; another tool's PreToolUse hook was dropped", commands)
	}
}

func TestMasterRegistrationKeepsTheTimeoutCodexCarries(t *testing.T) {
	dir := t.TempDir()
	if _, err := RegisterMaster("project", dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".codex", "hooks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Stand in for an operator who set a budget on the registered handler.
	edited := strings.Replace(string(raw), `"type": "command"`, `"timeout": 45,
            "type": "command"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterMaster("project", dir); err != nil {
		t.Fatal(err)
	}
	if got := MasterArming("project", dir).Timeout; got != 45 {
		t.Errorf("timeout = %d; want the 45 already on disk — steer owns the command, not the budget", got)
	}
}

func TestInstructionFileIsAgentsMD(t *testing.T) {
	got, err := InstructionFile("project", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("repo", "AGENTS.md"); got != want {
		t.Errorf("InstructionFile = %q; want %q", got, want)
	}

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	got, err = InstructionFile("global", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "AGENTS.md"); got != want {
		t.Errorf("global InstructionFile = %q; want %q", got, want)
	}
}
