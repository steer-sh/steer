package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/hookstore"
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

// installBlockingHook installs the no-var guard and returns the guard store.
func installBlockingHook(t *testing.T) string {
	t.Helper()
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var | use const or let",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}); err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir error: %v", err)
	}
	return hooksDir
}

// auditRow runs one blocking payload through the master and decodes the single
// row it writes.
func auditRow(t *testing.T, hooksDir, payload string) map[string]any {
	t.Helper()
	if out := runMasterHook(t, payload); !strings.Contains(out, `"block"`) {
		t.Fatalf("expected block decision, got: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(hooksDir, AuditLogName))
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("audit line is not valid JSON: %v\nline: %s", err, lines[len(lines)-1])
	}
	return entry
}

func TestAnAuditRowStampsTheGuardAndTheMasterItRanIn(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	hooksDir := installBlockingHook(t)

	entry := auditRow(t, hooksDir, `{"tool_name":"Bash","tool_input":{"command":"var x = 1"}}`)

	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}
	guardSource, err := os.ReadFile(filepath.Join(hooksDir, "no-var-hook.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry["hook_digest"], sourceDigest(guardSource); got != want {
		t.Errorf("hook_digest = %v; want %v", got, want)
	}
	if got, want := entry["rule_hash"], manifest.Hooks["no-var-hook"].RuleHash; got != want {
		t.Errorf("rule_hash = %v; want %v", got, want)
	}
	master, err := os.ReadFile(filepath.Join(hooksDir, MasterHookName))
	if err != nil {
		t.Fatal(err)
	}
	stamped, ok := entry["master_build_digest"].(string)
	if !ok || stamped == "" {
		t.Fatalf("master_build_digest missing: %v", entry["master_build_digest"])
	}
	// The master is hashed with the placeholder in place, so putting the
	// placeholder back reproduces the text the digest was taken over.
	placeheld := strings.Replace(string(master),
		`STEER_MASTER_BUILD_DIGEST = "`+stamped+`"`,
		`STEER_MASTER_BUILD_DIGEST = "`+masterBuildDigestPlaceholder+`"`, 1)
	if want := sourceDigest([]byte(placeheld)); stamped != want {
		t.Errorf("master_build_digest = %q; want %q", stamped, want)
	}
}

func TestAnAuditRowKeepsSuppliedLocatorsAndOmitsAbsentOnes(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	hooksDir := installBlockingHook(t)

	entry := auditRow(t, hooksDir, `{"tool_name":"Bash","tool_input":{"command":"var x = 1"},`+
		`"tool_use_id":"call-1","transcript_path":"/rollouts/root.jsonl"}`)

	locators, ok := entry["locators"].(map[string]any)
	if !ok {
		t.Fatalf("locators missing: %v", entry["locators"])
	}
	if got := locators["tool_use_id"]; got != "call-1" {
		t.Errorf("locators.tool_use_id = %v; want call-1", got)
	}
	if got := locators["transcript_path"]; got != "/rollouts/root.jsonl" {
		t.Errorf("locators.transcript_path = %v; want /rollouts/root.jsonl", got)
	}
	if _, present := locators["call_id"]; present {
		t.Errorf("locators names call_id, which the payload never supplied: %v", locators)
	}
}

func TestASmallBlockedInputIsRetainedInTheRow(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	hooksDir := installBlockingHook(t)

	command := "var x = 1; // " + strings.Repeat("y", 400)
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := auditRow(t, hooksDir, string(payload))

	if entry["evidence_complete"] != true {
		t.Fatalf("evidence_complete = %v; want true", entry["evidence_complete"])
	}
	evidence, ok := entry["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence missing: %v", entry["evidence"])
	}
	original, ok := evidence["payload"].(map[string]any)
	if !ok {
		t.Fatalf("evidence.payload missing: %v", evidence["payload"])
	}
	input, ok := original["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("evidence.payload.tool_input missing: %v", original["tool_input"])
	}
	// The snippet truncates at 200 characters; the evidence is the whole input.
	if got := input["command"]; got != command {
		t.Errorf("evidence holds %v; want the complete command", got)
	}
	if _, ok := evidence["edit"].(map[string]any); !ok {
		t.Fatalf("evidence.edit missing: %v", evidence["edit"])
	}
}

func TestALargeBlockedInputMovesToASidecarTheRowNames(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	hooksDir := installBlockingHook(t)

	command := "var x = 1; // " + strings.Repeat("z", 40000)
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := auditRow(t, hooksDir, string(payload))

	if _, inline := entry["evidence"]; inline {
		t.Errorf("a payload over the inline limit stayed in the row")
	}
	ref, ok := entry["evidence_ref"].(string)
	if !ok || ref == "" {
		t.Fatalf("evidence_ref missing: %v", entry["evidence_ref"])
	}
	data, err := os.ReadFile(filepath.Join(hooksDir, filepath.FromSlash(ref)))
	if err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	sum := sha256.Sum256(data)
	if got, want := entry["evidence_sha256"], hex.EncodeToString(sum[:]); got != want {
		t.Errorf("evidence_sha256 = %v; want %v", got, want)
	}
	if got, want := entry["evidence_bytes"], float64(len(data)); got != want {
		t.Errorf("evidence_bytes = %v; want %v", got, want)
	}
	var evidence map[string]any
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("sidecar is not valid JSON: %v", err)
	}
	original := evidence["payload"].(map[string]any)["tool_input"].(map[string]any)
	if got := original["command"]; got != command {
		t.Errorf("sidecar holds a different command than the call carried")
	}
}

func TestAFailedSidecarStillWritesTheRow(t *testing.T) {
	withProjectTempDir(t)
	requirePython3(t)
	hooksDir := installBlockingHook(t)

	// A file where the payload directory belongs makes every sidecar write fail.
	if err := os.WriteFile(filepath.Join(hooksDir, hookstore.PayloadsName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := "var x = 1; // " + strings.Repeat("z", 40000)
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := auditRow(t, hooksDir, string(payload))

	if entry["evidence_complete"] != false {
		t.Errorf("evidence_complete = %v; want false", entry["evidence_complete"])
	}
	if entry["evidence_error"] != "write" {
		t.Errorf("evidence_error = %v; want write", entry["evidence_error"])
	}
	if entry["snippet"] == "" || entry["hook_name"] != "no-var-hook" {
		t.Errorf("a firing whose evidence could not be retained lost its row: %v", entry)
	}
}
