//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// projectHookFiles are the project-scope harness files Steer registers into.
var projectHookFiles = []string{".claude/settings.json", ".codex/hooks.json"}

// HookCommands lists each registered command in a project harness file, in file
// order, with any directory part dropped so a command reads the same in every sandbox.
func (s *Sandbox) HookCommands(rel string) []string {
	s.t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	data, err := os.ReadFile(filepath.Join(s.Repo, rel))
	if err != nil {
		s.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		s.t.Fatalf("%s: %v", rel, err)
	}
	var out []string
	for _, event := range []string{"PreToolUse", "Stop", "SubagentStop"} {
		for _, group := range doc.Hooks[event] {
			for _, h := range group.Hooks {
				out = append(out, event+" "+filepath.Base(h.Command))
			}
		}
	}
	return out
}

// AddOperatorHook appends a PreToolUse hook the operator owns to a project harness file.
func (s *Sandbox) AddOperatorHook(rel string) {
	s.t.Helper()
	path := filepath.Join(s.Repo, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		s.t.Fatal(err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(pre, map[string]any{"matcher": "Bash",
		"hooks": []any{map[string]any{"type": "command", "command": "echo mine"}}})
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// FireMaster runs the project master on a Claude Code Bash payload and returns its
// decision and reason; an allowed call returns two empty strings.
func (s *Sandbox) FireMaster(command string) (decision, reason string) {
	s.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "session_id": "sess", "cwd": s.Repo,
	})
	r := s.Run(s.Repo, string(payload), nil, "python3", filepath.Join(s.Repo, ".steer", "hooks", "steer-hooks.py"))
	if r.Code != 0 {
		s.t.Fatalf("master exited %d:\n%s", r.Code, r.Output())
	}
	if strings.TrimSpace(r.Stdout) == "" {
		return "", ""
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		s.t.Fatalf("master output: %v\n%s", err, r.Stdout)
	}
	return out.HookSpecificOutput.PermissionDecision, out.HookSpecificOutput.PermissionDecisionReason
}

// TestRemovalLeavesOnlyTheOperatorsHooks installs every Steer artifact into both
// harnesses beside a hook the operator owns, removes each through its own command,
// and requires the operator's hook alone to remain, both instruction documents
// byte for byte as they were, and no Pi files on a machine with no Pi.
//
// pins existing behaviour
func TestRemovalLeavesOnlyTheOperatorsHooks(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	docs := map[string]string{"CLAUDE.md": "Project prose.\n", "AGENTS.md": "Codex prose.\n"}
	for name, text := range docs {
		s.Write(name, text)
	}
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := s.MustSteer("register", src, "--rule", noVarRule, "--all-harnesses"); strings.Contains(r.Output(), "Pi") {
		t.Errorf("register --all-harnesses names Pi on a machine with no Pi:\n%s", r.Output())
	}
	if fileExists(filepath.Join(s.Repo, ".pi")) {
		t.Error("register --all-harnesses wrote .pi/ on a machine with no Pi")
	}
	s.MustSteer("rules", "add", "config-path", "--rule", "resolve a config path from the repo root")
	for _, h := range []string{"claude-code", "codex"} {
		s.MustSteer("check", "install", "--harness", h)
		s.MustSteer("commit-gate", "install", "--harness", h)
	}
	s.MustSteer("statusline", "install", "--scope", "project")
	for _, rel := range projectHookFiles {
		s.AddOperatorHook(rel)
	}
	for _, rel := range projectHookFiles {
		if got := s.HookCommands(rel); len(got) < 5 {
			t.Fatalf("%s holds %v before removal, want the guard, gate, resolver and check runner", rel, got)
		}
	}

	s.MustSteer("rules", "remove", "config-path", "--reason", "e2e")
	s.MustSteer("remove", noVarHook, "--force", "--reason", "e2e")
	for _, h := range []string{"claude-code", "codex"} {
		s.MustSteer("check", "uninstall", "--harness", h)
	}
	for h, want := range map[string]string{"claude-code": "Removed Claude Code commit review hooks from ", "codex": "Removed Codex commit review hooks from "} {
		if r := s.MustSteer("commit-gate", "remove", "--harness", h, "--scope", "project"); !strings.Contains(r.Stdout, want) {
			t.Errorf("commit-gate remove --harness %s printed %q, want %q", h, r.Stdout, want)
		}
	}
	s.MustSteer("statusline", "remove", "--scope", "project")

	for _, rel := range projectHookFiles {
		if got, want := s.HookCommands(rel), []string{"PreToolUse echo mine"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s after every removal holds %v, want %v", rel, got, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(s.Repo, ".claude", "settings.json")); strings.Contains(string(data), "statusLine") {
		t.Errorf("the status line survived its removal:\n%s", data)
	}
	for name, text := range docs {
		if data, _ := os.ReadFile(filepath.Join(s.Repo, name)); string(data) != text {
			t.Errorf("%s after removal = %q, want %q", name, data, text)
		}
	}

	r := s.MustSteer("archive", "restore", noVarHook)
	if strings.Contains(r.Output(), "--supersedes") {
		t.Errorf("a restore with no fix record named one:\n%s", r.Output())
	}
	for _, rel := range projectHookFiles {
		got := strings.Join(s.HookCommands(rel), "\n")
		if !strings.Contains(got, "steer-hooks.py") {
			t.Errorf("%s after restore holds no master:\n%s", rel, got)
		}
	}
}

// TestSelfProtectionRefusesTheAgentAndNamesAWorkingCommand enables a factory preset,
// then requires the master to deny an agent's attempt to disable it, and the removal
// command the block names to work when the user runs it.
//
// pins existing behaviour
func TestSelfProtectionRefusesTheAgentAndNamesAWorkingCommand(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.MustSteer("preset", "enable", "eval-usage-security")

	decision, reason := s.FireMaster("steer preset disable eval-usage-security")
	if decision != "deny" {
		t.Fatalf("the master allowed an agent to disable a factory preset (%q)", decision)
	}
	const named = "preset disable steer-self-protection"
	if !strings.Contains(reason, named) {
		t.Fatalf("the block does not name %q:\n%s", named, reason)
	}

	s.MustSteer("preset", "disable", "steer-self-protection")
	s.MustSteer("preset", "disable", "eval-usage-security")
	for _, f := range []string{"steer-self-protection.py", "eval-usage-security.py"} {
		if fileExists(filepath.Join(s.Repo, ".steer", "hooks", f)) {
			t.Errorf("%s is still installed after the user disabled it", f)
		}
	}
}
