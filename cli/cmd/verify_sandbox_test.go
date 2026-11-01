package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

const verifyProbeGuard = `#!/usr/bin/env python3
import json, os, sys


def main():
    data = json.load(sys.stdin)
    content = data.get("tool_input", {}).get("content", "")
    if "BLOCKME" in content and os.path.exists("verify-marker"):
        print(json.dumps({"decision": "block", "reason": "marker says no"}))


if __name__ == "__main__":
    main()
`

// installVerifyProbe installs guard as the only guard of scope, in a fresh
// project directory and home, and returns the scope's master path.
func installVerifyProbe(t *testing.T, scope, guard string) string {
	t.Helper()
	dir := withCmdProjectTempDir(t)
	testhome.Set(t, t.TempDir())
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: "verify-probe",
		Rule:     "probe rule",
		Content:  []byte(guard),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}); err != nil {
		t.Fatalf("InstallManagedHook: %v", err)
	}
	master, err := claude.GetMasterHookPath(scope)
	if err != nil {
		t.Fatal(err)
	}
	return master
}

// A verification block is no live firing: the store's audit log and payload
// directory stay as they were, even for a payload large enough for a sidecar.
func TestVerifyLeavesTheLiveAuditLogUntouched(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		t.Run(scope, func(t *testing.T) {
			master := installVerifyProbe(t, scope, verifyProbeGuard)
			if err := os.WriteFile("verify-marker", nil, 0o644); err != nil {
				t.Fatal(err)
			}
			store := filepath.Dir(master)
			live := filepath.Join(store, "steer-audit.jsonl")
			sentinel := []byte("{\"sentinel\": true}\n")
			if err := os.WriteFile(live, sentinel, 0o600); err != nil {
				t.Fatal(err)
			}

			content := "BLOCKME\n" + strings.Repeat("x = 1\n", 4000)
			results, err := verifyAgainstMaster(master, []verifyTarget{{Path: "big.py", Content: content}})
			if err != nil {
				t.Fatalf("verifyAgainstMaster: %v", err)
			}
			got := results[0]
			if got.Err != nil || !got.Blocked {
				t.Fatalf("result = %+v, want a block", got)
			}
			if !strings.Contains(got.Reason, "marker says no") {
				t.Errorf("reason %q lost the guard's explanation", got.Reason)
			}
			if !strings.Contains(got.Reason, "Removing this hook needs the user's approval") {
				t.Errorf("reason %q lost the removal line", got.Reason)
			}
			if !got.FiringRemoved || strings.Contains(got.Reason, "contest") || strings.Contains(got.Reason, "firing:") {
				t.Errorf("reason %q still names the discarded firing", got.Reason)
			}

			if data, err := os.ReadFile(live); err != nil || !bytes.Equal(data, sentinel) {
				t.Errorf("live audit log = %q (%v), want it unchanged", data, err)
			}
			if _, err := os.Stat(filepath.Join(store, "steer-audit-payloads")); !os.IsNotExist(err) {
				t.Errorf("verification wrote a live payload directory (stat err %v)", err)
			}
		})
	}
}

// A guard that reads its own source, imports a sibling package reached through a
// relative symlink, loads a data file and lists an empty directory beside it
// behaves in the verification copy as it does in the live store.
func TestVerifyCopiesTheStoresGuardFiles(t *testing.T) {
	guard := `#!/usr/bin/env python3
import json, os, sys
import verify_probe_pkg

with open(os.path.join(os.path.dirname(__file__), "verify-probe-data", "policy.json")) as handle:
    POLICY = json.load(handle)
EXTRA = os.listdir(os.path.join(os.path.dirname(__file__), "policy.d"))


def main():
    json.load(sys.stdin)
    with open(__file__) as handle:
        own = handle.read()
    if verify_probe_pkg.BLOCK and POLICY["block"] and "OWN-SOURCE-TOKEN" in own:
        print(json.dumps({"decision": "block", "reason": "sibling says no"}))


if __name__ == "__main__":
    main()
`
	master := installVerifyProbe(t, "project", guard)
	store := filepath.Dir(master)
	files := map[string]string{
		filepath.Join("..", "..", "probe-pkg", "__init__.py"): "BLOCK = True\n",
		filepath.Join("verify-probe-data", "policy.json"):     `{"block": true}`,
	}
	for name, body := range files {
		path := filepath.Join(store, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "..", "probe-pkg"), filepath.Join(store, "verify_probe_pkg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(store, "policy.d"), 0o755); err != nil {
		t.Fatal(err)
	}

	results, err := verifyAgainstMaster(master, []verifyTarget{{Path: "a.py", Content: "x = 1\n"}})
	if err != nil {
		t.Fatalf("verifyAgainstMaster: %v", err)
	}
	if got := results[0]; got.Err != nil || !got.Blocked || !strings.Contains(got.Reason, "sibling says no") {
		t.Fatalf("result = %+v, want the sibling-dependent block", got)
	}
}

// A store reached through a symlink, holding a master that is itself a symlink,
// is mirrored whole.
func TestVerifyMirrorsALinkedStore(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `import json, os, sys
sys.stdin.read()
if os.path.exists(os.path.join(os.path.dirname(__file__), "data.txt")):
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "data says no"}}))
`
	if err := os.WriteFile(filepath.Join(root, "master-src.py"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "master-src.py"), filepath.Join(real, "steer-hooks.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "data.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	if err := os.Symlink(real, store); err != nil {
		t.Fatal(err)
	}

	results, err := verifyAgainstMaster(filepath.Join(store, "steer-hooks.py"), []verifyTarget{{Path: "a.py", Content: "x\n"}})
	if err != nil {
		t.Fatalf("verifyAgainstMaster: %v", err)
	}
	if got := results[0]; got.Err != nil || !got.Blocked || got.Reason != "data says no" {
		t.Fatalf("result = %+v, want the data-dependent block", got)
	}
}

// The firing lines are found by the recorded identifier, not by the footer's
// wording, and every other line survives.
func TestWithoutFiringDropsOnlyTheLinesNamingIt(t *testing.T) {
	id := strings.Repeat("ab", 16)
	reason := "explanation\n\n~~ hook x / ref " + id + "\nany wording " + id + " here\nRemove with: steer remove x"
	got, removed := withoutFiring(reason, id)
	if !removed || got != "explanation\n\nRemove with: steer remove x" {
		t.Fatalf("withoutFiring = %q, %v", got, removed)
	}
	if got, removed := withoutFiring(reason, ""); removed || got != reason {
		t.Fatalf("an unknown firing changed the reason: %q, %v", got, removed)
	}
}

// A run that records no row must not reuse the identifier an earlier run left in
// the log.
func TestVerifyReadsOnlyThisRunsFiring(t *testing.T) {
	dir := t.TempDir()
	old := strings.Repeat("cd", 16)
	master := filepath.Join(dir, "steer-hooks.py")
	script := `import json, sys
sys.stdin.read()
print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "why\n\nfiring: ` + old + `"}}))
`
	if err := os.WriteFile(master, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	row := `{"firing_id": "` + old + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "steer-audit.jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}

	got := runMasterHookVerification(master, verifyTarget{Path: "a.py", Content: "x\n"})
	if got.Err != nil || !got.Blocked {
		t.Fatalf("result = %+v, want a block", got)
	}
	if got.FiringRemoved || !strings.Contains(got.Reason, old) {
		t.Fatalf("result = %+v, want the reason kept whole", got)
	}
}

// A load warning on stderr does not hide the deny on stdout.
func TestVerifyReadsTheDecisionFromStdoutAlone(t *testing.T) {
	master := filepath.Join(t.TempDir(), "steer-hooks.py")
	script := `import json, sys
sys.stdin.read()
sys.stderr.write("steer: guard other not loaded: SyntaxError\n")
print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "no"}}))
`
	if err := os.WriteFile(master, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got := runMasterHookVerification(master, verifyTarget{Path: "a.py", Content: "x\n"})
	if got.Err != nil || !got.Blocked || got.Reason != "no" {
		t.Fatalf("result = %+v, want the deny", got)
	}
	if !strings.Contains(got.Stderr, "not loaded") {
		t.Errorf("stderr %q lost the load warning", got.Stderr)
	}
}
