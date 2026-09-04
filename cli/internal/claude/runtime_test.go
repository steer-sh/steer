package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// markerHookContent blocks on a marker in whichever field the virtual edit
// carries. It handles Edit, Write and Bash only, which is what a generated guard
// is written against — never MultiEdit or apply_patch.
const markerHookContent = `#!/usr/bin/env python3
import sys
import json

def main():
    data = json.load(sys.stdin)
    if data.get("tool_name") not in ("Edit", "Write", "Bash"):
        sys.exit(0)
    tool_input = data.get("tool_input", {})
    for key in ("command", "content", "new_string"):
        value = tool_input.get(key, "")
        if "FORBIDDEN" in value:
            print(json.dumps({
                "decision": "block",
                "continue": True,
                "stopReason": "forbidden in " + tool_input.get("file_path", "<no file>"),
                "systemMessage": "marker hook blocked a change",
            }))
            sys.exit(0)
    sys.exit(0)

if __name__ == "__main__":
    main()
`

// bashOnlyHookContent blocks on any Bash call at all. It proves apply_patch is
// never presented as a shell command: Codex carries a patch in the same
// tool_input.command a shell call uses.
const bashOnlyHookContent = `#!/usr/bin/env python3
import sys
import json

def main():
    data = json.load(sys.stdin)
    if data.get("tool_name") == "Bash":
        print(json.dumps({"decision": "block", "stopReason": "no shell"}))
    sys.exit(0)

if __name__ == "__main__":
    main()
`

func installGuard(t *testing.T, name, content string) {
	t.Helper()
	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: name,
		FileName: name + ".py",
		Rule:     "no FORBIDDEN",
		Content:  []byte(content),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook(%s) error: %v", name, err)
	}
}

func runMasterWithArgs(t *testing.T, payload string, args ...string) string {
	t.Helper()
	masterPath, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatalf("GetMasterHookPath error: %v", err)
	}
	cmd := exec.Command("python3", append([]string{masterPath}, args...)...)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("master hook failed: %v\noutput: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func readAudit(t *testing.T) []map[string]any {
	t.Helper()
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(hooksDir, AuditLogName))
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	rows := make([]map[string]any, 0, 1)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("audit line is not JSON: %v\nline: %s", err, line)
		}
		rows = append(rows, row)
	}
	return rows
}

// Every harness's tool shapes reach a guard written against
// Claude's Edit / Write / Bash contract.
func TestRuntimeNormalizesEveryToolShape(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	cases := []struct {
		name     string
		payload  string
		want     bool
		wantFile string
		why      string
	}{
		{"claude-bash", `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`,
			true, "", "a shell call passes through untouched"},
		{"claude-write", `{"tool_name":"Write","tool_input":{"file_path":"a.py","content":"FORBIDDEN"}}`,
			true, "a.py", "Write carries its body in content"},
		{"claude-edit", `{"tool_name":"Edit","tool_input":{"file_path":"a.py","new_string":"FORBIDDEN"}}`,
			true, "a.py", "Edit carries its body in new_string"},
		{"claude-multiedit", `{"tool_name":"MultiEdit","tool_input":{"file_path":"a.py","edits":[` +
			`{"new_string":"fine"},{"new_string":"FORBIDDEN"}]}}`,
			true, "a.py", "a guard that never handled MultiEdit sees every hunk of one"},
		{"claude-multiedit-clean", `{"tool_name":"MultiEdit","tool_input":{"file_path":"a.py","edits":[` +
			`{"new_string":"fine"},{"new_string":"also fine"}]}}`,
			false, "", "splitting a MultiEdit must not invent a block"},
		{"codex-bash", `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`,
			true, "", "Codex spells every shell path Bash too"},
		{"codex-apply-patch", `{"tool_name":"apply_patch","tool_input":{"command":` +
			`"*** Begin Patch\n*** Update File: one.py\n@@\n-old\n+clean\n*** Update File: two.py\n@@\n-old\n+FORBIDDEN\n*** End Patch"}}`,
			true, "two.py", "a multi-file patch is checked file by file"},
		{"codex-apply-patch-add", `{"tool_name":"apply_patch","tool_input":{"command":` +
			`"*** Begin Patch\n*** Add File: new.py\n+FORBIDDEN\n*** End Patch"}}`,
			true, "new.py", "an added file is a Write"},
		{"codex-apply-patch-removed-only", `{"tool_name":"apply_patch","tool_input":{"command":` +
			`"*** Begin Patch\n*** Update File: one.py\n@@\n-FORBIDDEN\n+clean\n context FORBIDDEN\n*** End Patch"}}`,
			false, "", "removed and context lines are what the file already survived"},
		{"codex-apply-patch-move", `{"tool_name":"apply_patch","tool_input":{"command":` +
			`"*** Begin Patch\n*** Update File: old.py\n*** Move to: new.py\n@@\n+FORBIDDEN\n*** End Patch"}}`,
			true, "new.py", "the rename target is the path that lands"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hooksDir, _ := GetHooksDir(ProjectScope)
			auditPath := filepath.Join(hooksDir, AuditLogName)
			_ = os.Remove(auditPath)

			out := runMasterWithArgs(t, tc.payload)
			blocked := strings.Contains(out, `"block"`)
			if blocked != tc.want {
				t.Fatalf("blocked = %v, want %v — %s\noutput: %s", blocked, tc.want, tc.why, out)
			}
			if !tc.want {
				return
			}
			rows := readAudit(t)
			if len(rows) != 1 {
				t.Fatalf("expected 1 audit row, got %d", len(rows))
			}
			if got := rows[0]["file_path"]; got != tc.wantFile {
				t.Fatalf("audit file_path = %v, want %q — %s", got, tc.wantFile, tc.why)
			}
		})
	}
}

// A patch reaches a guard as edits, never as the shell command its transport
// field would otherwise make it look like.
func TestRuntimeNeverPresentsAPatchAsAShellCommand(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	installGuard(t, "bash-only-hook", bashOnlyHookContent)

	out := runMasterWithArgs(t, `{"tool_name":"apply_patch","tool_input":{"command":`+
		`"*** Begin Patch\n*** Update File: one.py\n@@\n+x = 1\n*** End Patch"}}`)
	if out != "" {
		t.Fatalf("a Bash-only guard fired on a patch: %s", out)
	}
}

// The block reply is rendered in the harness's own deny contract.
func TestRuntimeRendersTheNativeDeny(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	payload := `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`

	claude := runMasterWithArgs(t, payload)
	var claudeReply map[string]any
	if err := json.Unmarshal([]byte(claude), &claudeReply); err != nil {
		t.Fatalf("Claude reply is not JSON: %v\n%s", err, claude)
	}
	if claudeReply["decision"] != "block" {
		t.Fatalf("Claude reply lost its block decision: %s", claude)
	}
	reason, _ := claudeReply["stopReason"].(string)
	if !strings.Contains(reason, "steer hook: marker-hook") {
		t.Fatalf("Claude stopReason lost the removal footer: %s", claude)
	}

	codex := runMasterWithArgs(t, payload, "--harness", "codex")
	var codexReply struct {
		Decision           any `json:"decision"`
		HookSpecificOutput *struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(codex), &codexReply); err != nil {
		t.Fatalf("Codex reply is not JSON: %v\n%s", err, codex)
	}
	if codexReply.HookSpecificOutput == nil {
		t.Fatalf("Codex reply carries no hookSpecificOutput: %s", codex)
	}
	if codexReply.HookSpecificOutput.PermissionDecision != "deny" ||
		codexReply.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("Codex deny malformed: %s", codex)
	}
	// Codex marks a hook errored when it returns a field the event does not
	// support, so Claude's own key must not ride along.
	if codexReply.Decision != nil {
		t.Fatalf("Codex reply carries Claude's decision key: %s", codex)
	}
	if !strings.Contains(codexReply.HookSpecificOutput.PermissionDecisionReason, "forbidden in") ||
		!strings.Contains(codexReply.HookSpecificOutput.PermissionDecisionReason, "steer hook: marker-hook") {
		t.Fatalf("Codex reason lost the guard's reason or the footer: %s", codex)
	}

	pi := runMasterWithArgs(t, payload, "--harness", "pi")
	var piReply struct {
		Block         bool   `json:"block"`
		Reason        string `json:"reason"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(pi), &piReply); err != nil {
		t.Fatalf("Pi reply is not JSON: %v\n%s", err, pi)
	}
	if !piReply.Block || !strings.Contains(piReply.Reason, "forbidden in") ||
		!strings.Contains(piReply.Reason, "steer hook: marker-hook") {
		t.Fatalf("Pi deny lost its block, reason, or footer: %s", pi)
	}
	if piReply.SystemMessage != "marker hook blocked a change" {
		t.Fatalf("Pi deny lost its systemMessage: %s", pi)
	}
}

func TestRuntimeAllowIsSilentOnEveryHarness(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	payload := `{"tool_name":"Bash","tool_input":{"command":"echo fine"}}`
	if out := runMasterWithArgs(t, payload); out != "" {
		t.Fatalf("Claude allow printed %q", out)
	}
	if out := runMasterWithArgs(t, payload, "--harness", "codex"); out != "" {
		t.Fatalf("Codex allow printed %q", out)
	}
	if out := runMasterWithArgs(t, payload, "--harness", "pi"); out != "" {
		t.Fatalf("Pi allow printed %q", out)
	}
}

// An audit row carries the joins a scorer needs: which harness, which root
// session, which thread, which repository.
func TestRuntimeAuditCarriesTheJoins(t *testing.T) {
	tempDir := withProjectTempDir(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	repo := filepath.Join(tempDir, "work")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "apply_patch",
		"tool_input": map[string]any{
			"command": "*** Begin Patch\n*** Update File: pkg/a.py\n@@\n+FORBIDDEN\n*** End Patch",
		},
		"session_id": "root-session",
		"agent_id":   "subagent-thread",
		"cwd":        nested,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	runMasterWithArgs(t, string(payload), "--harness", "codex")

	rows := readAudit(t)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	want := map[string]string{
		"harness":     "codex",
		"session_id":  "root-session",
		"thread_id":   "subagent-thread",
		"tool_name":   "apply_patch",
		"file_path":   "pkg/a.py",
		"enforcement": "block",
		"hook_name":   "marker-hook",
		"snippet":     "FORBIDDEN",
	}
	for key, expected := range want {
		got, _ := rows[0][key].(string)
		if got != expected {
			t.Fatalf("audit %q = %q, want %q", key, got, expected)
		}
	}
	// The repository is resolved by walking up from the call's cwd, so a call
	// made in a subdirectory still joins to the repo root.
	gotRepo, _ := rows[0]["repository"].(string)
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		resolved = repo
	}
	if gotRepo != repo && gotRepo != resolved {
		t.Fatalf("audit repository = %q, want %q", gotRepo, repo)
	}
	if ts, _ := rows[0]["timestamp"].(string); ts == "" {
		t.Fatalf("audit row has no timestamp")
	}
}
