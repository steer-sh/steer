package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// asCommittedClone leaves the store and removes what a clone without this
// machine's registrations lacks: the settings file and the instruction document.
func asCommittedClone(t *testing.T) {
	t.Helper()
	settings, _ := GetSettingsFile(ProjectScope)
	doc, _ := GetClaudeMDFile(ProjectScope)
	for _, path := range []string{settings, settings + BackupExtension, doc} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", path, err)
		}
	}
}

// ageMaster rewrites the master's stamp to an older runtime, so any regeneration
// of the master shows up as changed bytes.
func ageMaster(t *testing.T) string {
	t.Helper()
	master, err := hookstore.MasterPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, guard.StampPrefix) {
			lines[i] = guard.StampPrefix + `{"runtime":"deadbeef","version":"0.0.1"}`
		}
	}
	if err := os.WriteFile(master, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		t.Fatal(err)
	}
	return master
}

// storeBytes reads every file in the project store, keyed by name.
func storeBytes(t *testing.T) map[string]string {
	t.Helper()
	dir, err := hookstore.Resolve(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		out[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A clone of a repository whose guards were committed has the store and no
// registration. Sync registers the master the store holds, as it is, and writes
// the rules; it never regenerates the master or rewrites the manifest.
func TestSyncStoreRegistersTheCommittedMasterAsItIs(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	ageMaster(t)
	asCommittedClone(t)
	before := storeBytes(t)

	result, err := SyncStore(ProjectScope, Registrar{})
	if err != nil || result == nil {
		t.Fatalf("SyncStore: %#v, %v", result, err)
	}
	if len(result.Synced) != 1 || result.Hooks != 1 {
		t.Fatalf("result = %#v", result)
	}
	// The registration names the master relative to the repository root; ageMaster
	// returned the store path that names the same file.
	want, err := hookstore.MasterRegistration(ProjectScope, "")
	if err != nil {
		t.Fatalf("MasterRegistration returned error: %v", err)
	}
	registered, err := Registrar{}.RegisteredMasterPath(ProjectScope)
	if err != nil || registered != want {
		t.Fatalf("registered %q (%v), want %q", registered, err, want)
	}
	if !strings.Contains(readRules(t, ProjectScope), "- use python3 instead") {
		t.Fatal("the rules section was not written")
	}
	after := storeBytes(t)
	for path, data := range before {
		if after[path] != data {
			t.Errorf("sync rewrote %s", path)
		}
	}
	if len(after) != len(before) {
		t.Errorf("store files: %d before, %d after", len(before), len(after))
	}
}

// A master that does not embed the store's guards would run a guard set other
// than the one committed beside it, so sync refuses and names rebuild.
func TestSyncStoreRefusesAMasterThatDoesNotCarryTheStoresGuards(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	dir, err := hookstore.Resolve(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	writeTestHook(t, filepath.Join(dir, "added-by-hand.py"))
	asCommittedClone(t)

	_, err = SyncStore(ProjectScope, Registrar{})
	if err == nil || !strings.Contains(err.Error(), "added-by-hand is not in it") ||
		!strings.Contains(err.Error(), "steer hooks rebuild --scope project") {
		t.Fatalf("SyncStore error = %v", err)
	}
	if registered, _ := (Registrar{}).RegisteredMasterPath(ProjectScope); registered != "" {
		t.Fatalf("a refused sync registered %q", registered)
	}
}

// unreadableRules is Claude Code with an instruction path that cannot be read, so
// its rules section can only be rewritten.
type unreadableRules struct {
	Registrar
	wrote *bool
}

func (unreadableRules) InstructionPath(string) (string, error) {
	return "", fmt.Errorf("unreadable")
}

func (r unreadableRules) SetRules(rules []string, scope string) error {
	*r.wrote = true
	return nil
}

// A section that cannot be read is rewritten, even where the registration is
// already current: the failed read is a reason to write, not a sync failure.
func TestSyncStoreRewritesAnUnreadableSectionBesideACurrentRegistration(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	wrote := false

	result, err := SyncStore(ProjectScope, unreadableRules{wrote: &wrote})
	if err != nil || result == nil {
		t.Fatalf("SyncStore: %#v, %v", result, err)
	}
	if !wrote || len(result.Synced) != 1 {
		t.Fatalf("wrote %v, result %#v", wrote, result)
	}
}

// A target already in line with the store is not written at all: a project's
// settings file is shared, and a rewrite would be a diff nobody asked for.
func TestSyncStoreWritesNothingForATargetInSync(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	settings, _ := GetSettingsFile(ProjectScope)
	backup := settings + BackupExtension
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	result, err := SyncStore(ProjectScope, Registrar{})
	if err != nil || result == nil {
		t.Fatalf("SyncStore: %#v, %v", result, err)
	}
	if len(result.Synced) != 0 || len(result.Current) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("sync saved settings.json for a target already in sync")
	}
}
