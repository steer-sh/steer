package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// committedRepo returns a repository whose files are committed.
func committedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := buildRepo(t, files)
	gitCmd(t, root, verb, "-qm", "base")
	return root
}

func viewOf(t *testing.T, root string, spec DiffSpec) *CommitView {
	t.Helper()
	v, ok := NewCommitView(Defaults(), root, spec)
	if !ok {
		t.Fatal("NewCommitView: git did not answer")
	}
	return v
}

func readView(t *testing.T, v *CommitView, path string) string {
	t.Helper()
	data, err := v.Read(path)
	if err != nil {
		t.Fatalf("Read(%s): %v", path, err)
	}
	return string(data)
}

// TestCommitViewStagedReadsTheIndex: a staged-only commit lands the index, so an
// unstaged edit on top of a staged one is not what the view reads.
func TestCommitViewStagedReadsTheIndex(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "v1\n", "b.txt": "same\n"})
	writeFileAt(t, root, "a.txt", "v2\n")
	gitCmd(t, root, "add", "a.txt")
	writeFileAt(t, root, "a.txt", "v3\n")
	writeFileAt(t, root, "b.txt", "unstaged\n")
	writeFileAt(t, root, "new.txt", "untracked\n")

	v := viewOf(t, root, DiffSpec{StagedOnly: true})
	if got := readView(t, v, "a.txt"); got != "v2\n" {
		t.Errorf("a.txt = %q, want the staged v2", got)
	}
	if got := readView(t, v, "b.txt"); got != "same\n" {
		t.Errorf("b.txt = %q, want the index version", got)
	}
	if v.Has("new.txt") {
		t.Error("an untracked file the commit does not add is in the view")
	}
}

// TestCommitViewAllReadsTheWorkingTree: `commit -a` lands the working tree of every
// tracked file, and a tracked file removed from disk lands as a deletion.
func TestCommitViewAllReadsTheWorkingTree(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "v1\n", "gone.txt": "x\n", "c.txt": "c\n"})
	writeFileAt(t, root, "a.txt", "v3\n")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	v := viewOf(t, root, DiffSpec{})
	if got := readView(t, v, "a.txt"); got != "v3\n" {
		t.Errorf("a.txt = %q, want the working-tree v3", got)
	}
	if got := readView(t, v, "c.txt"); got != "c\n" {
		t.Errorf("c.txt = %q", got)
	}
	if _, err := v.Read("gone.txt"); !errors.Is(err, ErrNotInView) {
		t.Errorf("Read(gone.txt) err = %v, want ErrNotInView", err)
	}
	if want := []string{"a.txt", "c.txt"}; !slices.Equal(v.Paths(), want) {
		t.Errorf("Paths = %v, want %v", v.Paths(), want)
	}
}

// TestCommitViewNarrowedAddKeepsOtherFilesAtTheIndex: `git add a && git commit` lands
// a's working copy, while b's unstaged edit stays out.
func TestCommitViewNarrowedAddKeepsOtherFilesAtTheIndex(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "v1\n", "b.txt": "b1\n"})
	writeFileAt(t, root, "a.txt", "a2\n")
	writeFileAt(t, root, "b.txt", "b2\n")

	v := viewOf(t, root, DiffSpec{TrackedAddPathspecs: [][]string{{"a.txt"}}})
	if got := readView(t, v, "a.txt"); got != "a2\n" {
		t.Errorf("a.txt = %q, want the added working copy", got)
	}
	if got := readView(t, v, "b.txt"); got != "b1\n" {
		t.Errorf("b.txt = %q, want the index version", got)
	}
}

// TestCommitViewStagedPathOutsideTheAddKeepsItsIndexBlob: a staged edit the add does
// not name lands from the index, not from a later unstaged edit.
func TestCommitViewStagedPathOutsideTheAddKeepsItsIndexBlob(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a1\n", "b.txt": "b1\n"})
	writeFileAt(t, root, "a.txt", "a2\n")
	writeFileAt(t, root, "b.txt", "b2\n")
	gitCmd(t, root, "add", "b.txt")
	writeFileAt(t, root, "b.txt", "b3\n")

	v := viewOf(t, root, DiffSpec{TrackedAddPathspecs: [][]string{{"a.txt"}}})
	if got := readView(t, v, "b.txt"); got != "b2\n" {
		t.Errorf("b.txt = %q, want the staged b2", got)
	}
}

// TestCommitViewAllReadsAWorkingCopyRestoredToHead: -a over a staged edit whose
// working copy is back at HEAD lands HEAD's content, which no diff against HEAD names.
func TestCommitViewAllReadsAWorkingCopyRestoredToHead(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "v1\n"})
	writeFileAt(t, root, "a.txt", "v2\n")
	gitCmd(t, root, "add", "a.txt")
	writeFileAt(t, root, "a.txt", "v1\n")

	v := viewOf(t, root, DiffSpec{})
	if got := readView(t, v, "a.txt"); got != "v1\n" {
		t.Errorf("a.txt = %q, want the working copy v1", got)
	}
}

// TestCommitViewAddsOnlyTheNamedUntrackedFiles: the untracked files the command adds
// read from disk; another untracked file stays out.
func TestCommitViewAddsOnlyTheNamedUntrackedFiles(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a\n"})
	writeFileAt(t, root, "new.txt", "fresh\n")
	writeFileAt(t, root, "other.txt", "not added\n")

	v := viewOf(t, root, DiffSpec{StagedOnly: true, IncludeUntracked: true,
		UntrackedAddPathspecs: [][]string{{"new.txt"}}})
	if got := readView(t, v, "new.txt"); got != "fresh\n" {
		t.Errorf("new.txt = %q", got)
	}
	if v.Has("other.txt") {
		t.Error("an untracked file no add names is in the view")
	}
}

// TestCommitViewResolvesAnAddFromItsSubdirectory: `git add new.txt` run in sub adds
// sub/new.txt, not a root file of the same name.
func TestCommitViewResolvesAnAddFromItsSubdirectory(t *testing.T) {
	root := committedRepo(t, map[string]string{"sub/a.txt": "a\n"})
	writeFileAt(t, root, "sub/new.txt", "in sub\n")
	writeFileAt(t, root, "new.txt", "at root\n")

	v := viewOf(t, filepath.Join(root, "sub"), DiffSpec{StagedOnly: true, IncludeUntracked: true,
		UntrackedAddPathspecs: [][]string{{"new.txt"}}})
	if got := readView(t, v, "sub/new.txt"); got != "in sub\n" {
		t.Errorf("sub/new.txt = %q", got)
	}
	if v.Has("new.txt") {
		t.Error("the root file of the same name is in the view")
	}
}

// TestCommitViewReadsTheListedIndexBlob: a path staged again after the view was
// built reads the blob the view listed.
func TestCommitViewReadsTheListedIndexBlob(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "v1\n"})
	v := viewOf(t, root, DiffSpec{StagedOnly: true})
	writeFileAt(t, root, "a.txt", "v2\n")
	gitCmd(t, root, "add", "a.txt")
	if got := readView(t, v, "a.txt"); got != "v1\n" {
		t.Errorf("a.txt = %q, want the listed v1", got)
	}
}

// TestCommitViewKeepsAddedCruft: OS cruft an add names lands, so the view holds it.
func TestCommitViewKeepsAddedCruft(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a\n"})
	writeFileAt(t, root, ".DS_Store", "finder\n")
	v := viewOf(t, root, DiffSpec{StagedOnly: true, IncludeUntracked: true,
		UntrackedAddPathspecs: [][]string{{".DS_Store"}}})
	if !v.Has(".DS_Store") {
		t.Error("an added .DS_Store is not in the view")
	}
}

// TestCommitViewLeavesOutAnIntentToAdd: a plain commit leaves out an add -N entry,
// and -a lands its working copy.
func TestCommitViewLeavesOutAnIntentToAdd(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a\n"})
	writeFileAt(t, root, "new.txt", "intent\n")
	gitCmd(t, root, "add", "-N", "new.txt")
	if viewOf(t, root, DiffSpec{StagedOnly: true}).Has("new.txt") {
		t.Error("a staged-only view holds the intent-to-add entry")
	}
	if got := readView(t, viewOf(t, root, DiffSpec{}), "new.txt"); got != "intent\n" {
		t.Errorf("-a: new.txt = %q, want the working copy", got)
	}
}

// TestCommitViewLeavesOutAnIntentToAddThatMatchesADeletion: an intent-to-add entry
// identical to a deleted tracked file is still left out, not read as a rename.
func TestCommitViewLeavesOutAnIntentToAddThatMatchesADeletion(t *testing.T) {
	body := "the same content in both files, long enough to pair as a rename\n"
	root := committedRepo(t, map[string]string{"old.txt": body})
	if err := os.Remove(filepath.Join(root, "old.txt")); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "new.txt", body)
	gitCmd(t, root, "add", "-N", "new.txt")
	if viewOf(t, root, DiffSpec{StagedOnly: true}).Has("new.txt") {
		t.Error("a staged-only view holds the intent-to-add entry")
	}
}

// TestCommitViewRefusesAnInTreeSymlinkedParent: a tracked directory replaced by a
// symlink to another directory of the work tree lands as a deletion of its files.
func TestCommitViewRefusesAnInTreeSymlinkedParent(t *testing.T) {
	root := committedRepo(t, map[string]string{"dir/a.txt": "dir\n", "other/a.txt": "other\n"})
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if v := viewOf(t, root, DiffSpec{}); v.Has("dir/a.txt") {
		t.Error("the view reads dir/a.txt through the symlinked parent")
	}
}

// TestCommitViewScanReadsBothSourcesWithinTheLimit: one scan returns index and
// working-tree content in path order, and a file over the limit comes without data.
func TestCommitViewScanReadsBothSourcesWithinTheLimit(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a1\n", "b.txt": "b1\n", "big.txt": "0123456789\n", "c.txt": "c1\n"})
	writeFileAt(t, root, "a.txt", "a2\n")
	v := viewOf(t, root, DiffSpec{})
	var got []string
	err := v.Scan(context.Background(), []string{"a.txt", "b.txt", "big.txt", "missing.txt", "c.txt"}, 5,
		func(path string, data []byte, size int64) bool {
			got = append(got, fmt.Sprintf("%s=%q/%d", path, data, size))
			return true
		})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`a.txt="a2\n"/3`, `b.txt="b1\n"/3`, `big.txt=""/11`, `c.txt="c1\n"/3`}
	if !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

// TestCommitViewHoldsNoSymlink: a tracked symlink and an added symlink to a directory
// outside the repository are not files in the view.
func TestCommitViewHoldsNoSymlink(t *testing.T) {
	outside := t.TempDir()
	writeFileAt(t, outside, "secret.txt", "secret\n")
	root := committedRepo(t, map[string]string{"a.txt": "a\n"})
	if err := os.Symlink("a.txt", filepath.Join(root, "tracked-link")); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "add", "tracked-link")
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}

	v := viewOf(t, root, DiffSpec{IncludeUntracked: true})
	for _, path := range []string{"tracked-link", "out", "out/secret.txt"} {
		if _, err := v.Read(path); !errors.Is(err, ErrNotInView) {
			t.Errorf("Read(%s) err = %v, want ErrNotInView", path, err)
		}
	}
}

// TestCommitViewDiskReadStaysInTheWorkTree: a file that a symlinked directory
// replaced after the view was built does not lead the read out of the work tree.
func TestCommitViewDiskReadStaysInTheWorkTree(t *testing.T) {
	outside := t.TempDir()
	writeFileAt(t, outside, "a.txt", "secret\n")
	root := committedRepo(t, map[string]string{"dir/a.txt": "v1\n"})
	writeFileAt(t, root, "dir/a.txt", "v2\n")
	v := viewOf(t, root, DiffSpec{})

	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if data, err := v.Read("dir/a.txt"); err == nil {
		t.Errorf("Read followed a symlinked parent out of the work tree: %q", data)
	}
}
