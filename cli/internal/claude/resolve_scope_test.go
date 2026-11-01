package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// twoStores points HOME at one temp directory and the working directory at
// another, so the global and project guard stores resolve to different trees.
// It returns both hook directories.
func twoStores(t *testing.T) (projectDir, globalDir string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	projectDir, err = GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	globalDir, err = GetHooksDir(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(projectDir, globalDir) {
		t.Fatalf("the two stores resolved to one directory: %s", projectDir)
	}
	return projectDir, globalDir
}

func writeHook(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte("import sys\nsys.exit(0)\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestAMissNamesTheScopeHoldingTheHook: the default scope is project, so a
// global guard answered `steer test <hook>` with a bare "no hooks found" and the
// operator had no way to learn --scope global exists.
func TestAMissNamesTheScopeHoldingTheHook(t *testing.T) {
	_, globalDir := twoStores(t)
	writeHook(t, globalDir, "no-bare-python")

	_, err := ResolveHook(ProjectScope, "no-bare-python")
	if err == nil {
		t.Fatal("the project store holds no hook, so the lookup must miss")
	}
	if !errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("a hook in the other scope must carry ErrHookInOtherScope: %v", err)
	}
	if !strings.Contains(err.Error(), "--scope global") {
		t.Fatalf("the miss must name the flag that reaches it: %v", err)
	}
}

// TestAMissFromTheGlobalStoreNamesTheProjectScope: the hint is symmetric, so an
// operator who passed --scope global for a project guard learns the same way.
func TestAMissFromTheGlobalStoreNamesTheProjectScope(t *testing.T) {
	projectDir, _ := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")

	_, err := ResolveHook(GlobalScope, "no-bare-python")
	if !errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("a hook in the other scope must carry ErrHookInOtherScope: %v", err)
	}
	if !strings.Contains(err.Error(), "--scope project") {
		t.Fatalf("the miss must name the flag that reaches it: %v", err)
	}
}

// TestAnEmptyStoreNamesTheScopeItRead: with nothing installed anywhere, the
// reader still learns which store was read and that a second one exists.
func TestAnEmptyStoreNamesTheScopeItRead(t *testing.T) {
	twoStores(t)

	_, err := ResolveHook(ProjectScope, "no-bare-python")
	if err == nil {
		t.Fatal("an empty store must miss")
	}
	if errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("no store holds the hook, so nothing may point at a scope: %v", err)
	}
	if !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "--scope global") {
		t.Fatalf("the miss must name the store it read and the other one: %v", err)
	}
}

// TestAnUnknownNameBesideInstalledHooksNamesTheStore: a store holding other
// guards reports the miss against that store rather than pointing elsewhere.
func TestAnUnknownNameBesideInstalledHooksNamesTheStore(t *testing.T) {
	projectDir, _ := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")

	_, err := ResolveHook(ProjectScope, "some-other-guard")
	if err == nil {
		t.Fatal("an unknown identifier must miss")
	}
	if errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("no store holds this identifier: %v", err)
	}
	if !strings.Contains(err.Error(), "project") {
		t.Fatalf("the miss must name the store it read: %v", err)
	}
}

// TestTheReadScopeWinsOverTheOtherOne: both stores holding the name must resolve
// to the one asked for, or the hint would reach a guard the operator did not name.
func TestTheReadScopeWinsOverTheOtherOne(t *testing.T) {
	projectDir, globalDir := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")
	writeHook(t, globalDir, "no-bare-python")

	hook, err := ResolveHook(GlobalScope, "no-bare-python")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hook.Path, globalDir) {
		t.Fatalf("the global read resolved to %s, outside %s", hook.Path, globalDir)
	}
}

// ownershipStore seeds the project store with guard files holding their own stems
// and a manifest of key to file.
func ownershipStore(t *testing.T, files []string, entries map[string]string) string {
	t.Helper()
	dir, _ := twoStores(t)
	for _, file := range files {
		writeHook(t, dir, file)
	}
	manifest := &Manifest{Hooks: map[string]ManifestEntry{}}
	for key, file := range entries {
		manifest.Hooks[key] = ManifestEntry{File: file, Rule: "rule of " + key}
	}
	if err := SaveManifest(ProjectScope, manifest); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An exact key resolves to its entry's file past an unowned file, and past a file
// another guard owns, that carries the key as a stem.
func TestAnExactKeyResolvesToItsEntrysFile(t *testing.T) {
	ownershipStore(t, []string{"a", "z", "c", "d"}, map[string]string{
		"a": "z.py",
		"b": "c.py",
		"c": "d.py",
	})
	for id, want := range map[string]string{"a": "z.py", "c": "d.py", "b": "c.py"} {
		hook, err := ResolveHook(ProjectScope, id)
		if err != nil {
			t.Fatalf("ResolveHook(%s): %v", id, err)
		}
		if hook.Name != id || hook.FileName != want {
			t.Errorf("ResolveHook(%s) = %s in %s, want %s in %s", id, hook.Name, hook.FileName, id, want)
		}
	}
}

// Every entry sharing a file resolves under its own key, so a replay or removal
// by key reads that entry's fixtures.
func TestASharingKeyResolvesUnderItsOwnID(t *testing.T) {
	ownershipStore(t, []string{"s"}, map[string]string{"a": "s.py", "b": "s.py"})
	hook, err := ResolveHook(ProjectScope, "b")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Name != "b" || hook.FileName != "s.py" {
		t.Errorf("got %s in %s, want b in s.py", hook.Name, hook.FileName)
	}
}

// A key whose file is gone is an error for a reader; the unowned file with its
// stem never stands in. A removal still targets the file the entry names.
func TestAKeyWhoseFileIsGoneNeverResolvesToItsStem(t *testing.T) {
	dir := ownershipStore(t, []string{"a"}, map[string]string{"a": "z.py"})
	if hook, err := ResolveHook(ProjectScope, "a"); err == nil {
		t.Fatalf("ResolveHook returned %s, want an error", hook.FileName)
	} else if !strings.Contains(err.Error(), "z.py") {
		t.Errorf("the error names no file: %v", err)
	}
	hook, err := ResolveRemoval(ProjectScope, "a")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Name != "a" || hook.Path != filepath.Join(dir, "z.py") {
		t.Errorf("ResolveRemoval = %s at %s, want a at z.py", hook.Name, hook.Path)
	}
}

// The file's own stem still reaches the guard that owns it.
func TestAnOwnedStemResolvesToItsOwner(t *testing.T) {
	ownershipStore(t, []string{"a", "z"}, map[string]string{"a": "z.py"})
	hook, err := ResolveHook(ProjectScope, "z")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Name != "a" || hook.FileName != "z.py" {
		t.Errorf("got %s in %s, want a in z.py", hook.Name, hook.FileName)
	}
}

// An unowned file named outright is refused while its stem is another guard's id;
// without the refusal its removal consumes that guard's entry and orphans its file.
func TestAnUnownedFileUnderAnotherGuardsIDIsRefused(t *testing.T) {
	dir := ownershipStore(t, []string{"a", "z"}, map[string]string{"a": "z.py"})
	if hook, err := ResolveHook(ProjectScope, "a.py"); !errors.Is(err, ErrUnownedGuardFile) {
		t.Fatalf("ResolveHook(a.py) = %s, %v; want ErrUnownedGuardFile", hook.FileName, err)
	}
	if _, err := RemoveManagedHook(ProjectScope, "a.py", RemoveOptions{}); !errors.Is(err, ErrUnownedGuardFile) {
		t.Fatalf("RemoveManagedHook(a.py) = %v, want ErrUnownedGuardFile", err)
	}
	for _, file := range []string{"a.py", "z.py"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("%s is gone: %v", file, err)
		}
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Hooks["a"]; !ok {
		t.Error("the refused removal dropped entry a")
	}
}

// An unowned file whose stem is no key stays addressable.
func TestAnUnownedFileWithItsOwnStemResolves(t *testing.T) {
	ownershipStore(t, []string{"loose", "z"}, map[string]string{"a": "z.py"})
	hook, err := ResolveHook(ProjectScope, "loose")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Name != "loose" || hook.FileName != "loose.py" {
		t.Errorf("got %s in %s", hook.Name, hook.FileName)
	}
}

// A miss names the other scope for any key it holds, including one that shares a
// file with a lower id and so owns no listed file.
func TestAMissNamesTheScopeHoldingASharingKey(t *testing.T) {
	_, globalDir := twoStores(t)
	writeHook(t, globalDir, "s")
	manifest := &Manifest{Hooks: map[string]ManifestEntry{
		"a": {File: "s.py", Rule: "rule of a"},
		"b": {File: "s.py", Rule: "rule of b"},
	}}
	if err := SaveManifest(GlobalScope, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveHook(ProjectScope, "b"); !errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("ResolveHook(b) = %v, want ErrHookInOtherScope", err)
	}
}

// A removal by key refuses an entry naming the manifest or the master, and deletes
// neither; a stem file the entry does not own is reported as staying.
func TestARemovalRefusesAnEntryNamingANonGuardFile(t *testing.T) {
	dir := ownershipStore(t, []string{"a"}, map[string]string{
		"m": hookstore.ManifestName,
		"h": MasterHookName,
	})
	for _, id := range []string{"m", "h", "h.py"} {
		if _, err := RemoveManagedHook(ProjectScope, id, RemoveOptions{}); !errors.Is(err, ErrNotAGuardFile) {
			t.Fatalf("RemoveManagedHook(%s) = %v, want ErrNotAGuardFile", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, hookstore.ManifestName)); err != nil {
		t.Errorf("the manifest is gone: %v", err)
	}
}

// StrayStemFile names only an existing file no entry owns under the key's own stem.
func TestStrayStemFile(t *testing.T) {
	dir := ownershipStore(t, []string{"a", "z", "b", "y"}, map[string]string{
		"a": "z.py",
		"b": "b.py",
		"c": "y.py",
		strings.TrimSuffix(MasterHookName, ".py"): "y.py",
	})
	master := strings.TrimSuffix(MasterHookName, ".py")
	if err := os.WriteFile(filepath.Join(dir, MasterHookName), []byte("# master\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"a": "a.py", "b": "", "c": "", "gone": "", "z": "", master: ""} {
		if got := StrayStemFile(ProjectScope, id); got != want {
			t.Errorf("StrayStemFile(%s) = %q, want %q", id, got, want)
		}
	}
}
