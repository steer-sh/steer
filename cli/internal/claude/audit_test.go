package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const blockingHookContent = `#!/usr/bin/env python3
import sys
import json

def main():
    data = json.load(sys.stdin)
    command = data.get("tool_input", {}).get("command", "")
    if "var " in command:
        print(json.dumps({"decision": "block", "reason": "never use var"}))
        sys.exit(0)
    sys.exit(0)

if __name__ == "__main__":
    main()
`

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func runMasterHook(t *testing.T, payload string) string {
	t.Helper()
	masterPath, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatalf("GetMasterHookPath error: %v", err)
	}
	cmd := exec.Command("python3", masterPath)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("master hook execution failed: %v\noutput: %s", err, out)
	}
	return string(out)
}

func TestMasterHookWritesAuditLineOnBlock(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)

	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var | use const or let",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}

	output := runMasterHook(t, `{"tool_name":"Bash","tool_input":{"command":"var x = 1"}}`)
	if !strings.Contains(output, `"block"`) {
		t.Fatalf("expected block decision, got: %s", output)
	}

	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir error: %v", err)
	}
	auditPath := filepath.Join(hooksDir, AuditLogName)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 audit line, got %d: %s", len(lines), data)
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("audit line is not valid JSON: %v\nline: %s", err, lines[0])
	}

	expectations := map[string]string{
		"rule":        "never use var | use const or let",
		"hook_name":   "no-var-hook",
		"enforcement": "block",
		"file_path":   "",
		"tool_name":   "Bash",
		"snippet":     "var x = 1",
	}
	for key, want := range expectations {
		got, ok := entry[key].(string)
		if !ok {
			t.Fatalf("audit field %q missing or not a string: %v", key, entry[key])
		}
		if got != want {
			t.Fatalf("audit field %q = %q, want %q", key, got, want)
		}
	}
	if ts, ok := entry["timestamp"].(string); !ok || ts == "" {
		t.Fatalf("audit timestamp missing: %v", entry["timestamp"])
	}
}

func TestMasterHookNoAuditLineOnAllow(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)

	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}

	runMasterHook(t, `{"tool_name":"Bash","tool_input":{"command":"const x = 1"}}`)

	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, AuditLogName)); !os.IsNotExist(err) {
		t.Fatalf("expected no audit log on allow, stat err: %v", err)
	}
}
