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
	if !masterBlocked(output) {
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
	if out := runMasterHook(t, payload); !masterBlocked(out) {
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

func forcePortableAuditWrites(t *testing.T, masterPath string) {
	t.Helper()
	master, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	master = []byte(strings.Replace(string(master), "def steer_descriptor_writes():", "def steer_descriptor_writes():\n    return False", 1))
	if err := os.WriteFile(masterPath, master, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMasterAuditRefusesSymlinkedLogAndStore(t *testing.T) {
	for _, component := range []string{"log", "dangling-log", "hooks", "parent", "log-portable", "dangling-log-portable", "hooks-portable", "parent-portable"} {
		t.Run(component, func(t *testing.T) {
			withProjectTempDir(t)
			requirePython3(t)
			hooksDir := installBlockingHook(t)
			if strings.HasSuffix(component, "-portable") {
				forcePortableAuditWrites(t, filepath.Join(hooksDir, MasterHookName))
				component = strings.TrimSuffix(component, "-portable")
			}
			outside := t.TempDir()
			target := filepath.Join(outside, AuditLogName)
			original := []byte("unrelated data\n")
			if component != "dangling-log" {
				if err := os.WriteFile(target, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(hooksDir, AuditLogName)
			switch component {
			case "hooks", "parent":
				link = hooksDir
				if component == "parent" {
					link = filepath.Dir(hooksDir)
				}
				moved := link + "-original"
				if err := os.Rename(link, moved); err != nil {
					t.Fatal(err)
				}
				// Keep the generated master executable through the linked path.
				master, err := os.ReadFile(filepath.Join(moved, MasterHookName))
				if component == "parent" {
					master, err = os.ReadFile(filepath.Join(moved, "hooks", MasterHookName))
					if mkdirErr := os.Mkdir(filepath.Join(outside, "hooks"), 0o700); mkdirErr != nil {
						t.Fatal(mkdirErr)
					}
					target = filepath.Join(outside, "hooks", AuditLogName)
					if writeErr := os.WriteFile(target, original, 0o600); writeErr != nil {
						t.Fatal(writeErr)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(target), MasterHookName), master, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			if output := runMasterHook(t, `{"tool_name":"Bash","tool_input":{"command":"var x = 1"}}`); !masterBlocked(output) {
				t.Fatalf("audit refusal weakened the guard: %s", output)
			}
			got, err := os.ReadFile(target)
			if component == "dangling-log" {
				if !os.IsNotExist(err) {
					t.Fatalf("dangling log link created its target: %v, %s", err, got)
				}
			} else if err != nil || string(got) != string(original) {
				t.Fatalf("audit write escaped through %s: %v, %s", component, err, got)
			}
		})
	}
}

// An audit log that ends in half a row, as a crash mid-append leaves it, must not
// swallow the next firing's row.
func TestMasterAuditRowAfterATornLineParses(t *testing.T) {
	for _, portable := range []bool{false, true} {
		t.Run(map[bool]string{false: "descriptors", true: "portable"}[portable], func(t *testing.T) {
			withProjectTempDir(t)
			requirePython3(t)
			hooksDir := installBlockingHook(t)
			if portable {
				forcePortableAuditWrites(t, filepath.Join(hooksDir, MasterHookName))
			}
			auditPath := filepath.Join(hooksDir, AuditLogName)
			if err := os.WriteFile(auditPath, []byte(`{"hook_name":"torn`), 0o600); err != nil {
				t.Fatal(err)
			}
			if output := runMasterHook(t, `{"tool_name":"Bash","tool_input":{"command":"var x = 1"}}`); !masterBlocked(output) {
				t.Fatalf("expected block decision, got: %s", output)
			}
			data, err := os.ReadFile(auditPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			var entry map[string]any
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil || entry["hook_name"] != "no-var-hook" {
				t.Errorf("the firing's row does not parse on its own line (%v):\n%s", err, data)
			}
		})
	}
}

// pins existing behaviour: a global .steer link is selected by the operator.
func TestGlobalAuditKeepsOperatorSelectedStoreLink(t *testing.T) {
	for _, portable := range []bool{false, true} {
		t.Run(map[bool]string{false: "descriptors", true: "portable"}[portable], func(t *testing.T) {
			withProjectTempDir(t)
			requirePython3(t)
			home := spacedHome(t)
			storeParent := t.TempDir()
			if err := os.Symlink(storeParent, filepath.Join(home, ".steer")); err != nil {
				t.Fatal(err)
			}
			if _, err := InstallManagedHook(GlobalScope, InstallManagedHookInput{
				HookName: "no-var-hook", FileName: "no-var-hook.py", Rule: "never use var",
				Content: []byte(blockingHookContent), Metadata: HookInstallMetadata{Source: SourceGenerated},
			}); err != nil {
				t.Fatal(err)
			}
			masterPath, err := GetMasterHookPath(GlobalScope)
			if err != nil {
				t.Fatal(err)
			}
			if portable {
				forcePortableAuditWrites(t, masterPath)
			}
			for _, size := range []int{10, 40000} {
				payload, err := json.Marshal(map[string]any{
					"tool_name": "Bash", "tool_input": map[string]any{"command": "var " + strings.Repeat("z", size)},
				})
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("python3", masterPath)
				cmd.Stdin = strings.NewReader(string(payload))
				if output, err := cmd.CombinedOutput(); err != nil || !masterBlocked(string(output)) {
					t.Fatalf("global master failed: %v, %s", err, output)
				}
			}
			data, err := os.ReadFile(filepath.Join(storeParent, "hooks", AuditLogName))
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var row map[string]any
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, row)
			}
			if len(rows) != 2 {
				t.Fatalf("linked global store lost audit rows: %v", rows)
			}
			for _, row := range rows {
				if row["evidence_complete"] != true {
					t.Fatalf("linked global store lost evidence: %v", row)
				}
				if ref, ok := row["evidence_ref"].(string); ok {
					if _, err := os.Stat(filepath.Join(storeParent, "hooks", filepath.FromSlash(ref))); err != nil {
						t.Fatalf("global sidecar absent: %v", err)
					}
				}
			}
		})
	}
}

func TestMasterEvidenceRefusesSymlinkedPaths(t *testing.T) {
	for _, component := range []string{"directory", "temporary", "target", "directory-portable", "temporary-portable", "target-portable"} {
		t.Run(component, func(t *testing.T) {
			withProjectTempDir(t)
			requirePython3(t)
			hooksDir := installBlockingHook(t)
			if strings.HasSuffix(component, "-portable") {
				forcePortableAuditWrites(t, filepath.Join(hooksDir, MasterHookName))
				component = strings.TrimSuffix(component, "-portable")
			}
			outside := t.TempDir()
			original := []byte("unrelated data\n")
			target := filepath.Join(outside, "unrelated.txt")
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			// A fixed firing identifier makes the sidecar paths deterministic.
			masterPath := filepath.Join(hooksDir, MasterHookName)
			master, err := os.ReadFile(masterPath)
			if err != nil {
				t.Fatal(err)
			}
			master = []byte(strings.Replace(string(master), "return os.urandom(16).hex()", `return "fixed-firing"`, 1))
			if err := os.WriteFile(masterPath, master, 0o600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(hooksDir, hookstore.PayloadsName)
			link := directory
			if component == "directory" {
				target = outside
			} else {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(directory, "fixed-firing.json")
				if component == "temporary" {
					link += ".part"
				}
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"tool_name": "Bash", "tool_input": map[string]any{"command": "var " + strings.Repeat("z", 40000)},
			})
			if err != nil {
				t.Fatal(err)
			}
			entry := auditRow(t, hooksDir, string(payload))
			if entry["evidence_complete"] != false || entry["evidence_error"] != "write" {
				t.Fatalf("unsafe evidence path was accepted: %v", entry)
			}
			if component == "directory" {
				files, err := os.ReadDir(outside)
				if err != nil || len(files) != 1 {
					t.Fatalf("evidence escaped into linked directory: %v, %v", files, err)
				}
			} else {
				got, err := os.ReadFile(target)
				if err != nil || string(got) != string(original) {
					t.Fatalf("evidence overwrote linked target: %v, %s", err, got)
				}
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("refused link was replaced: %v, %v", info, err)
			}
		})
	}
}
