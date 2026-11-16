package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// legacyStore writes a guard store at the legacy path and registers the master
// there, without going through any steer entry point — the shape of a machine
// installed before the store moved.
func legacyStore(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".claude", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create legacy store: %v", err)
	}
	writeTestHook(t, filepath.Join(dir, "no-var-hook.py"))

	manifest := Manifest{Hooks: map[string]ManifestEntry{
		"no-var-hook": {File: "no-var-hook.py", Rule: "never use var", Created: "2026-01-01"},
	}}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal the manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o644); err != nil {
		t.Fatalf("failed to write the manifest: %v", err)
	}

	master := filepath.Join(dir, MasterHookName)
	if err := os.WriteFile(master, []byte("#!/usr/bin/env python3\n# an older steer wrote this\n"), 0o755); err != nil {
		t.Fatalf("failed to write the legacy master: %v", err)
	}

	settings := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"python3 ` +
		filepath.ToSlash(master) + `"}]}]}}`)
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}
	return master
}

// TestRebuildRegistersTheFileItJustWrote is the whole point of the command.
// Consolidation migrates a legacy store and writes the master to the neutral
// path, and migration never deletes, so regenerating alone leaves the harness
// registered on the stale legacy file and the repaired one unused.
func TestRebuildRegistersTheFileItJustWrote(t *testing.T) {
	root := withProjectTempDir(t)
	legacyMaster := legacyStore(t, root)

	result, err := RebuildMaster(ProjectScope)
	if err != nil {
		t.Fatalf("RebuildMaster returned error: %v", err)
	}
	if !result.Migrated {
		t.Error("the legacy store did not report as migrated")
	}
	if result.Hooks != 1 {
		t.Fatalf("hooks: got %d, want 1", result.Hooks)
	}

	registered, err := Registrar{}.RegisteredMasterPath(ProjectScope)
	if err != nil {
		t.Fatalf("RegisteredMasterPath returned error: %v", err)
	}
	// The registration is relative and legacyMaster is absolute, so they are
	// compared as paths rather than as strings.
	if SamePath(registered, legacyMaster) {
		t.Fatal("settings.json still invokes the legacy master")
	}
	// A registration names the master relative to the repository root, which is
	// the form MasterRegistration renders.
	neutral, err := hookstore.MasterRegistration(ProjectScope, "")
	if err != nil {
		t.Fatalf("MasterRegistration returned error: %v", err)
	}
	if registered != neutral {
		t.Fatalf("registered master: got %q, want %q", registered, neutral)
	}
	if !result.Moved() {
		t.Error("the result does not report the registration moving")
	}

	// The registered file is the rebuilt one, stamped with this binary's runtime.
	stamp, ok := guard.ReadStamp(registered)
	if !ok {
		t.Fatal("the registered master carries no runtime stamp")
	}
	if stamp.Runtime != guard.RuntimeDigest() {
		t.Errorf("runtime: got %q, want %q", stamp.Runtime, guard.RuntimeDigest())
	}

	// The legacy file stays where it is, for an older binary, and stays stale.
	if _, ok := guard.ReadStamp(legacyMaster); ok {
		t.Error("the legacy master was rewritten; migration must never delete or edit it")
	}
}

// TestRegisteredMasterPathSkipsOtherEntries pins that an unrelated PreToolUse
// entry ahead of the master does not read as "no master registered".
func TestRegisteredMasterPathSkipsOtherEntries(t *testing.T) {
	root := withProjectTempDir(t)
	master := filepath.Join(root, ".steer", "hooks", MasterHookName)
	settings := []byte(`{"hooks":{"PreToolUse":[` +
		`{"matcher":"Bash","hooks":[{"type":"command","command":"steer commit-gate review"}]},` +
		`{"matcher":"Edit","hooks":[{"type":"command","command":"python3 ` + filepath.ToSlash(master) + `"}]}]}}`)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("failed to create .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}

	got, err := Registrar{}.RegisteredMasterPath(ProjectScope)
	if err != nil {
		t.Fatalf("RegisteredMasterPath returned error: %v", err)
	}
	if got != master {
		t.Fatalf("registered master: got %q, want %q", got, master)
	}
}

// TestRebuildDeregistersAnEmptyStore pins the answer when the store holds no
// guards: there is no master to point at, so the registration comes out.
func TestRebuildDeregistersAnEmptyStore(t *testing.T) {
	root := withProjectTempDir(t)
	legacyStore(t, root)
	if err := os.Remove(filepath.Join(root, ".claude", "hooks", "no-var-hook.py")); err != nil {
		t.Fatalf("failed to remove the guard: %v", err)
	}

	result, err := RebuildMaster(ProjectScope)
	if err != nil {
		t.Fatalf("RebuildMaster returned error: %v", err)
	}
	if result.Hooks != 0 {
		t.Fatalf("hooks: got %d, want 0", result.Hooks)
	}
	registered, err := Registrar{}.RegisteredMasterPath(ProjectScope)
	if err != nil {
		t.Fatalf("RegisteredMasterPath returned error: %v", err)
	}
	if registered != "" {
		t.Fatalf("settings.json still invokes %q after the last guard went", registered)
	}
}

// TestGeneratedMasterCarriesTheStamp pins that consolidation stamps every master
// it writes, which is what makes staleness readable at all.
func TestGeneratedMasterCarriesTheStamp(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		Rule:     "never use var",
		Content:  []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n"),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}); err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatalf("GetMasterHookPath returned error: %v", err)
	}
	content, err := os.ReadFile(master)
	if err != nil {
		t.Fatalf("failed to read the master: %v", err)
	}
	if !strings.Contains(string(content), guard.StampPrefix) {
		t.Fatal("the generated master carries no stamp line")
	}
	stamp, ok := guard.ParseStamp(string(content))
	if !ok || stamp.Runtime != guard.RuntimeDigest() {
		t.Errorf("stamp: got %+v, want runtime %s", stamp, guard.RuntimeDigest())
	}
}
