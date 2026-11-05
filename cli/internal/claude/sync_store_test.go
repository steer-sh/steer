package claude

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// asCommittedClone leaves the store and removes what a clone without this
// machine's registrations lacks: the settings file and the instruction document.
func asCommittedClone(t *testing.T) {
	t.Helper()
	settings, _ := GetSettingsFile(ProjectScope)
	doc, _ := GetClaudeMDFile(ProjectScope)
	for _, path := range []string{settings, doc} {
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
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(settings, past, past); err != nil {
		t.Fatal(err)
	}

	result, err := SyncStore(ProjectScope, Registrar{})
	if err != nil || result == nil {
		t.Fatalf("SyncStore: %#v, %v", result, err)
	}
	if len(result.Synced) != 0 || len(result.Current) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if info, err := os.Stat(settings); err != nil || !info.ModTime().Equal(past) {
		t.Fatal("sync saved settings.json for a target already in sync")
	}
}

// runRegisteredMaster runs the registered command through a shell as Claude Code
// does: in dir, with the variable naming root.
func runRegisteredMaster(t *testing.T, root, dir string) ([]byte, error) {
	t.Helper()
	command, err := Registrar{}.RegisteredMasterCommand(ProjectScope)
	if err != nil || command == "" {
		t.Fatalf("registered command %q, %v", command, err)
	}
	run := exec.Command("sh", "-c", command)
	run.Dir = dir
	run.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+root)
	run.Stdin = strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"x"}}`)
	return run.CombinedOutput()
}

// A session's cd into a subdirectory moves the directory a hook runs in. The guard
// must still run there, or python3's exit 2 blocks every tool call.
func TestARegisteredMasterRunsFromASubdirectory(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "cli", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runRegisteredMaster(t, root, sub); err != nil {
		t.Fatalf("the master failed from a subdirectory: %v\n%s", err, out)
	}
}

// A registration written before the anchor names the same path, so only the command
// text shows it needs the rewrite. Sync repoints it and a second sync changes nothing.
func TestSyncStoreRepointsABareProjectRegistration(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	const timeout = 77
	for i := range settings.Hooks.PreToolUse {
		for j := range settings.Hooks.PreToolUse[i].Hooks {
			settings.Hooks.PreToolUse[i].Hooks[j].Command = "python3 .steer/hooks/" + MasterHookName
			settings.Hooks.PreToolUse[i].Hooks[j].Timeout = timeout
		}
	}
	if err := SaveSettings(ProjectScope, settings); err != nil {
		t.Fatal(err)
	}
	if stale, err := MasterCommandStale(ProjectScope); err != nil || !stale {
		t.Fatalf("MasterCommandStale = %v, %v; want true", stale, err)
	}

	result, err := SyncStore(ProjectScope, Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Synced) != 1 {
		t.Fatalf("result = %#v; want one synced target", result)
	}
	want, err := GetMasterHookCommand(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := (Registrar{}).RegisteredMasterCommand(ProjectScope); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	settings, err = LoadSettings(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Hooks.PreToolUse[0].Hooks[0].Timeout; got != timeout {
		t.Fatalf("timeout = %d, want %d", got, timeout)
	}

	again, err := SyncStore(ProjectScope, Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Synced) != 0 || len(again.Current) != 1 {
		t.Fatalf("second sync = %#v; want the target current", again)
	}
}
