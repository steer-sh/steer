package hookstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const manifest = `{"hooks":{"no-var":{"file":"no-var-hook.py"}}}`

// projectRoot changes into a temporary directory, which project scope resolves
// its relative paths against.
func projectRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if runtime.GOOS == "darwin" {
		// t.TempDir hands back /var, a symlink to /private/var.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
	}
	return dir
}

func writeLegacy(t *testing.T, files map[string]string) {
	t.Helper()
	dir, err := LegacyDir("project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestResolveIsNeutralOnAnEmptyMachine(t *testing.T) {
	projectRoot(t)
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".steer", "hooks"); got != want {
		t.Errorf("Resolve = %q; want %q", got, want)
	}
}

func TestResolveReadsALegacyStoreThatNeverMigrated(t *testing.T) {
	projectRoot(t)
	writeLegacy(t, map[string]string{ManifestName: manifest})
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".claude", "hooks"); got != want {
		t.Errorf("Resolve = %q; want %q", got, want)
	}
}

func TestPrepareMovesTheStoreAndLeavesEveryOtherHookAlone(t *testing.T) {
	projectRoot(t)
	writeLegacy(t, map[string]string{
		ManifestName:     manifest,
		MasterName:       "print('master')\n",
		AuditName:        "{}\n",
		"no-var-hook.py": "print('guard')\n",
		"check-gate.py":  "print('someone else')\n",
		"notes-index.sh": "echo hi\n",
	})

	dir, err := Prepare("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".steer", "hooks"); dir != want {
		t.Fatalf("Prepare = %q; want %q", dir, want)
	}
	for name, want := range map[string]string{
		ManifestName:     manifest,
		MasterName:       "print('master')\n",
		AuditName:        "{}\n",
		"no-var-hook.py": "print('guard')\n",
	} {
		if got := read(t, filepath.Join(dir, name)); got != want {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
	}
	for _, name := range []string{"check-gate.py", "notes-index.sh"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was copied; the store owns only what its manifest names", name)
		}
	}

	// The copy never deletes, so an older binary still finds its store.
	legacy, err := LegacyDir("project")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(legacy, MasterName)); got != "print('master')\n" {
		t.Errorf("legacy master was disturbed: %q", got)
	}

	// Resolve reads the neutral store from here on, and never falls back again.
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("Resolve = %q after migration; want %q", got, dir)
	}
}

func TestPrepareLeavesAMigratedStoreUntouched(t *testing.T) {
	projectRoot(t)
	writeLegacy(t, map[string]string{ManifestName: manifest, MasterName: "old\n"})
	dir, err := Prepare("project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MasterName), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare("project"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, MasterName)); got != "new\n" {
		t.Errorf("master = %q; a second Prepare overwrote the migrated store", got)
	}
}

func TestAnInterruptedMigrationIsRetriedWhole(t *testing.T) {
	projectRoot(t)
	writeLegacy(t, map[string]string{ManifestName: manifest, "no-var-hook.py": "guard\n"})

	// A read-only neutral directory stands in for a write that fails partway. The
	// manifest must not land, or the retry below reads the store as migrated.
	neutral, err := Dir("project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(neutral, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(neutral, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(neutral, 0o755) })

	if _, err := Prepare("project"); err == nil {
		t.Fatal("Prepare reported success over a failed write")
	}
	if _, err := os.Stat(filepath.Join(neutral, ManifestName)); !os.IsNotExist(err) {
		t.Fatal("the manifest landed before the rest of the store, so the retry will skip it")
	}

	if err := os.Chmod(neutral, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare("project"); err != nil {
		t.Fatalf("the retry did not migrate: %v", err)
	}
	if got := read(t, filepath.Join(neutral, ManifestName)); got != manifest {
		t.Errorf("manifest = %q; want the legacy one", got)
	}
	if got := read(t, filepath.Join(neutral, "no-var-hook.py")); got != "guard\n" {
		t.Errorf("guard = %q; want the legacy one", got)
	}
}

func TestMigrationNeverOverwritesAnAuthoredGuard(t *testing.T) {
	root := projectRoot(t)
	writeLegacy(t, map[string]string{ManifestName: manifest, "no-var-hook.py": "legacy\n"})

	// The store is where the skill writes a new guard before registering it, so a
	// same-named file in the neutral store is the newer one.
	neutral := filepath.Join(root, ".steer", "hooks")
	if err := os.MkdirAll(neutral, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(neutral, "no-var-hook.py"), []byte("rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Prepare("project"); err != nil {
		t.Fatalf("Prepare refused an authored guard: %v", err)
	}
	if got := read(t, filepath.Join(neutral, "no-var-hook.py")); got != "rewritten\n" {
		t.Errorf("guard = %q; the migration overwrote the authored file", got)
	}
	if got := read(t, filepath.Join(neutral, ManifestName)); got != manifest {
		t.Errorf("manifest = %q; the rest of the store did not migrate", got)
	}
	// The legacy copy is still readable, so nothing was lost.
	legacy, err := LegacyDir("project")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(legacy, "no-var-hook.py")); got != "legacy\n" {
		t.Errorf("legacy guard = %q; want it untouched", got)
	}
}

func TestGlobalScopeSitsUnderTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	resolved, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Dir("global")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolved, ".steer", "hooks"); got != want {
		t.Errorf("Dir = %q; want %q", got, want)
	}
}

func TestAnInvalidScopeIsAnError(t *testing.T) {
	if _, err := Dir("session"); err == nil {
		t.Error("Dir accepted an unknown scope")
	}
}

// TestPrepareKeepsTheAuditLogOutOfGit runs git itself, so the entry is tested by
// what git ignores and not by the file's spelling.
func TestPrepareKeepsTheAuditLogOutOfGit(t *testing.T) {
	root := projectRoot(t)
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	dir, err := Prepare("project")
	if err != nil {
		t.Fatal(err)
	}
	for name, ignored := range map[string]bool{AuditName: true, ManifestName: false, MasterName: false} {
		err := exec.Command("git", "-C", root, "check-ignore", "-q", filepath.Join(dir, name)).Run()
		if got := err == nil; got != ignored {
			t.Errorf("git ignores %s = %v; want %v", name, got, ignored)
		}
	}
}

func TestPrepareAddsTheAuditEntryToAnExistingGitignoreOnce(t *testing.T) {
	projectRoot(t)
	dir, err := Dir("project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, IgnoreName)
	if err := os.WriteFile(path, []byte("fixtures/scratch"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Prepare("project"); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := read(t, path), "fixtures/scratch\n"+AuditName+"\n"; got != want {
		t.Errorf("%s = %q; want %q", IgnoreName, got, want)
	}
}
