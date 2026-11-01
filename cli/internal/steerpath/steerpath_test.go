package steerpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateDirIsUnderTheNeutralHome(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	got := StateDir("commit-review")
	want := filepath.Join(home, ".steer", "state", "commit-review")
	if got != want {
		t.Errorf("StateDir = %q; want %q", got, want)
	}
	got = LegacyStateDir("commit-review")
	want = filepath.Join(home, ".claude", "state", "commit-review")
	if got != want {
		t.Errorf("LegacyStateDir = %q; want %q", got, want)
	}
}

func TestConfigFileIsUnderTheNeutralHome(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	got := ConfigFile()
	want := filepath.Join(home, ".steer", "config.json")
	if got != want {
		t.Errorf("ConfigFile = %q; want %q", got, want)
	}
}

// initRepoWithSub builds a repository with one subdirectory and returns both,
// resolved the way the filesystem reports them.
func initRepoWithSub(t *testing.T) (root, sub string) {
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

// A project store belongs to its repository, so a command run from a
// subdirectory reads and writes the store the repository root holds.
func TestProjectScopeAnchorsAtTheRepositoryRoot(t *testing.T) {
	root, sub := initRepoWithSub(t)

	t.Chdir(root)
	fromRoot, err := HooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	fromSub, err := HooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot != fromSub {
		t.Errorf("store from a subdirectory = %q; want the root's %q", fromSub, fromRoot)
	}
	if want := filepath.Join(root, ".steer", "hooks"); fromSub != want {
		t.Errorf("store = %q; want %q", fromSub, want)
	}

	got, err := ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("ProjectRoot from a subdirectory = %q; want %q", got, root)
	}
}

// Outside a repository the working directory is the anchor, so a directory that
// is no repository still has a project store.
func TestProjectScopeOutsideARepositoryAnchorsAtTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(resolved)

	root, err := ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root != resolved {
		t.Errorf("ProjectRoot outside a repository = %q; want the working directory %q", root, resolved)
	}
}

// A git object path is repository-relative, so the committed store's path never
// depends on where a reader stands.
func TestProjectHooksRelIsRepositoryRelative(t *testing.T) {
	_, sub := initRepoWithSub(t)
	t.Chdir(sub)

	rel := ProjectHooksRel()
	if len(rel) != 2 {
		t.Fatalf("ProjectHooksRel = %v; want the neutral and legacy stores", rel)
	}
	if rel[0] != filepath.Join(".steer", "hooks") || rel[1] != filepath.Join(".claude", "hooks") {
		t.Errorf("ProjectHooksRel = %v", rel)
	}
	for _, p := range rel {
		if filepath.IsAbs(p) {
			t.Errorf("%q is absolute; a git object path is repository-relative", p)
		}
	}
}

// Project scope bounds a link by the working directory's project root; global
// scope leaves it unbounded.
func TestLinkBoundIsTheProjectRootOnlyUnderProjectScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := LinkBound("project"); err != nil || got != physical {
		t.Fatalf("project bound: %q, %v; want %q", got, err, physical)
	}
	if got, err := LinkBound("global"); err != nil || got != "" {
		t.Fatalf("global bound: %q, %v; want none", got, err)
	}
}

// Within places a path by where its links lead, and places a path that does not
// exist yet by its directory.
func TestWithinPlacesAPathWhereItsLinksLead(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	mustWrite := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustLink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repo, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(filepath.Join(repo, "real", "file.json"))
	mustWrite(filepath.Join(outside, "file.json"))
	mustLink(filepath.Join(outside, "file.json"), filepath.Join(repo, "out-file.json"))
	mustLink(outside, filepath.Join(repo, "out-dir"))
	mustLink(filepath.Join(repo, "real", "file.json"), filepath.Join(repo, "in-file.json"))
	mustLink(filepath.Join(outside, "missing.json"), filepath.Join(repo, "dangling.json"))

	for _, tc := range []struct {
		path string
		want bool
	}{
		{filepath.Join(repo, "real", "file.json"), true},
		{filepath.Join(repo, "real", "new.json"), true},
		{filepath.Join(repo, "new-dir", "new.json"), true},
		{filepath.Join(repo, "in-file.json"), true},
		{filepath.Join(repo, "out-file.json"), false},
		{filepath.Join(repo, "out-dir", "new.json"), false},
		{filepath.Join(repo, "dangling.json"), false},
		{filepath.Join(outside, "file.json"), false},
	} {
		if got := Within(repo, tc.path); got != tc.want {
			t.Errorf("Within(repo, %s) = %v, want %v", filepath.Base(filepath.Dir(tc.path))+"/"+filepath.Base(tc.path), got, tc.want)
		}
	}
}
