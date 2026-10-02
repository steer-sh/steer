package hookstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	root := projectRoot(t)
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".steer", "hooks"); got != want {
		t.Errorf("Resolve = %q; want %q", got, want)
	}
}

func TestResolveReadsALegacyStoreThatNeverMigrated(t *testing.T) {
	root := projectRoot(t)
	writeLegacy(t, map[string]string{ManifestName: manifest})
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".claude", "hooks"); got != want {
		t.Errorf("Resolve = %q; want %q", got, want)
	}
}

func TestPrepareMovesTheStoreAndLeavesEveryOtherHookAlone(t *testing.T) {
	root := projectRoot(t)
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
	if want := filepath.Join(root, ".steer", "hooks"); dir != want {
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
	payload := filepath.Join(PayloadsName, "0123.json")
	for name, ignored := range map[string]bool{AuditName: true, payload: true, ManifestName: false, MasterName: false} {
		err := exec.Command("git", "-C", root, "check-ignore", "-q", filepath.Join(dir, name)).Run()
		if got := err == nil; got != ignored {
			t.Errorf("git ignores %s = %v; want %v", name, got, ignored)
		}
	}
}

func TestPrepareAddsTheEvidenceEntriesToAnExistingGitignoreOnce(t *testing.T) {
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
	if got, want := read(t, path), "fixtures/scratch\n"+AuditName+"\n"+PayloadsName+"/\n"; got != want {
		t.Errorf("%s = %q; want %q", IgnoreName, got, want)
	}
}

// repoWithSub builds a repository with one subdirectory, both resolved.
func repoWithSub(t *testing.T) (root, sub string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %s", out)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sub = filepath.Join(resolved, "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return resolved, sub
}

// A mutation from a subdirectory writes the repository's store, and a read from
// the root finds it. The two directories held separate stores before.
func TestPrepareFromASubdirectoryWritesTheRepositorysStore(t *testing.T) {
	root, sub := repoWithSub(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	dir, err := Prepare("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".steer", "hooks"); dir != want {
		t.Fatalf("Prepare from a subdirectory = %q; want %q", dir, want)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("project")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("Resolve from the root = %q; want the store the subdirectory wrote %q", got, dir)
	}
}

// A registration must resolve against the directory holding the harness config,
// which every harness still writes at the working directory. From the repository
// root that is the store's own path; from a subdirectory it climbs to it.
func TestMasterRegistrationResolvesFromTheDirectoryItIsWrittenIn(t *testing.T) {
	root, sub := repoWithSub(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	fromRoot, err := MasterRegistration("project", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".steer", "hooks", MasterName); fromRoot != want {
		t.Fatalf("MasterRegistration at the root = %q; want %q", fromRoot, want)
	}

	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	fromSub, err := MasterRegistration("project", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("..", ".steer", "hooks", MasterName); fromSub != want {
		t.Fatalf("MasterRegistration in a subdirectory = %q; want %q", fromSub, want)
	}
	// Both name the one master the store holds.
	store, err := MasterPath("project")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(fromSub)
	if err != nil {
		t.Fatal(err)
	}
	if abs != store {
		t.Errorf("the registration resolves to %q; want the store's master %q", abs, store)
	}

	global, err := MasterRegistration("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(global) {
		t.Errorf("a global registration = %q; want an absolute path", global)
	}
}

// A registration is computed for the repository holding the config directory,
// not for the one the process stands in. A caller that names another checkout,
// such as a harness registrar handed a project directory, gets that checkout's
// master.
func TestMasterRegistrationUsesTheConfigDirectorysRepository(t *testing.T) {
	other, _ := repoWithSub(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	here, _ := repoWithSub(t)
	if err := os.Chdir(here); err != nil {
		t.Fatal(err)
	}

	got, err := MasterRegistration("project", other)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".steer", "hooks", MasterName); got != want {
		t.Fatalf("MasterRegistration for another checkout = %q; want %q", got, want)
	}
	if filepath.IsAbs(got) || strings.HasPrefix(got, "..") {
		t.Errorf("the registration escapes its own checkout: %q", got)
	}
}

// A config directory named through a symlink still resolves against the physical
// root git reports, so the registration does not climb to the filesystem root.
func TestMasterRegistrationResolvesASymlinkedConfigDirectory(t *testing.T) {
	root, _ := repoWithSub(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got, err := MasterRegistration("project", alias)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(".steer", "hooks", MasterName); got != want {
		t.Fatalf("MasterRegistration through an alias = %q; want %q", got, want)
	}
}

// TestMasterPathFromCommandReadsAQuotedPath: the registration is the only record
// of which master a harness runs, so a path that needed quoting has to come back
// whole. A command outside the written shape names no master at all, because a
// fragment of a path cannot be told from a short one.
func TestMasterPathFromCommandReadsAQuotedPath(t *testing.T) {
	spaced := "/Users/two words/.steer/hooks/" + MasterName
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"a quoted path", "python3 '" + spaced + "' --harness codex", spaced},
		{"an unquoted path an older binary wrote", "python3 /home/j/.steer/hooks/" + MasterName, "/home/j/.steer/hooks/" + MasterName},
		{"a project registration", "python3 .steer/hooks/" + MasterName, ".steer/hooks/" + MasterName},
		{"a windows registration", `python3 "C:/Program Files/.steer/hooks/` + MasterName + `"`, "C:/Program Files/.steer/hooks/" + MasterName},
		{"an unterminated quote", "python3 '/Users/two words/.steer/hooks/" + MasterName, ""},
		{"no master", "python3 /home/j/.claude/hooks/read-provenance.py", ""},
		{"the interpreter alone", "python3", ""},
		{"an empty command", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MasterPathFromCommand(tc.command); got != tc.want {
				t.Fatalf("MasterPathFromCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrepareAddsThePayloadEntryToAStoreOlderThanIt covers the store written
// before payload sidecars existed: its .gitignore names the audit log alone, and
// the entry it is missing is added without disturbing the one it has.
func TestPrepareAddsThePayloadEntryToAStoreOlderThanIt(t *testing.T) {
	projectRoot(t)
	dir, err := Dir("project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, IgnoreName)
	if err := os.WriteFile(path, []byte(AuditName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare("project"); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, path), AuditName+"\n"+PayloadsName+"/\n"; got != want {
		t.Errorf("%s = %q; want %q", IgnoreName, got, want)
	}
}

// A store file that is a symlink is refused, and the file it names is left as
// it was.
func TestWriteFileRefusesALinkedFile(t *testing.T) {
	store := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, "guard.py")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("guard\n"), 0o755); err == nil {
		t.Fatal("WriteFile wrote through a linked file")
	}
	if got := read(t, outside); got != "kept\n" {
		t.Fatalf("the outside file changed: %q", got)
	}
}

// A store subdirectory that is a symlink is refused, so no file lands at its
// target.
func TestWriteFileRefusesALinkedDirectory(t *testing.T) {
	store := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(store, "fixtures")); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(filepath.Join(store, "fixtures", "guard.json"), []byte("{}\n"), 0o644); err == nil {
		t.Fatal("WriteFile wrote through a linked directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "guard.json")); !os.IsNotExist(err) {
		t.Fatalf("a file landed outside the store: %v", err)
	}
}

// A project's .steer or hooks directory that is a symlink is refused before
// Prepare creates or writes anything at its target.
func TestPrepareRefusesALinkedProjectStore(t *testing.T) {
	for _, linked := range []string{".steer", filepath.Join(".steer", "hooks")} {
		t.Run(linked, func(t *testing.T) {
			root := projectRoot(t)
			outside := t.TempDir()
			at := filepath.Join(root, linked)
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, at); err != nil {
				t.Fatal(err)
			}

			if _, err := Prepare("project"); err == nil {
				t.Fatalf("Prepare accepted a linked %s", linked)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("Prepare wrote %d entries at the link's target", len(entries))
			}
		})
	}
}
