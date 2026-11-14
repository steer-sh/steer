package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestChangedPathsSplitsRenamesAndDecodesQuotedBinaries(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "foo.py"), []byte("def f():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "foo.py")
	git(t, dir, "commit", "-qm", "one")
	git(t, dir, "mv", "foo.py", "bar.py")

	diff := "diff --git \"a/caf\\303\\251.png\" \"b/caf\\303\\251.png\"\n" +
		"Binary files \"a/caf\\303\\251.png\" and \"b/caf\\303\\251.png\" differ\n"
	got, err := changedPaths(dir, "HEAD", diff)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"foo.py", "bar.py", "café.png"} {
		if !slices.Contains(got, want) {
			t.Errorf("changedPaths = %q, missing %q", got, want)
		}
	}
}
