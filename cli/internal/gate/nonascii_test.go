package gate

import (
	"strings"
	"testing"
)

// quotingRepo is buildRepo with git's default path quoting pinned on, so an
// operator's global `core.quotepath=false` cannot hide a reader that skips -z.
func quotingRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := buildRepo(t, files)
	gitCmd(t, root, "config", "core.quotepath", "true")
	return root
}

// TestNonASCIIUntrackedFileIsReviewed: a new file whose name git would C-quote must
// reach the reviewer with its hunks. A quoted name cannot be opened, so a reader that
// skips -z leaves the raw diff empty and the commit passes as having nothing to review.
func TestNonASCIIUntrackedFileIsReviewed(t *testing.T) {
	cfg := Defaults()
	root := quotingRepo(t, map[string]string{"old.go": "package x\n"})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "café.go", "package x\n\nfunc Brew() {}\n")

	spec := DiffSpec{IncludeUntracked: true}
	result, ok := GetDiffSized(cfg, root, spec)
	if !ok {
		t.Fatal("GetDiffSized failed")
	}
	if !strings.Contains(result.Raw, "+func Brew() {}") {
		t.Errorf("the new file's hunks must be in the raw diff, got %q", result.Raw)
	}
	if !result.Complete {
		t.Error("a file that rendered must not mark the diff incomplete")
	}
	if strings.Contains(result.Text, "Excluded from the diff") {
		t.Error("a reviewable file must not be named as generated and dropped")
	}

	paths, ok := ChangedFiles(cfg, root, spec)
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	if !equalStrings(sortedUnique(paths), []string{"café.go"}) {
		t.Errorf("ChangedFiles = %q, want the path as it is on disk", paths)
	}
}

// TestNonASCIITrackedPathsReadUnquoted: the tracked name lists feed the noise filter,
// the prose and binary checks, and the budget's dropped-file preamble, which all
// compare or show the real path.
func TestNonASCIITrackedPathsReadUnquoted(t *testing.T) {
	cfg := Defaults()
	root := quotingRepo(t, map[string]string{
		"über/package-lock.json": "{}\n",
		"naïve.go":               "package x\n",
		"small.go":               "package x\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "über/package-lock.json", strings.Repeat("{\"a\": 1}\n", 50))
	writeFileAt(t, root, "naïve.go", "package x\n"+strings.Repeat("var a = 1\n", cfg.MaxDiffChars/8))
	writeFileAt(t, root, "small.go", "package x\n\nvar b = 2\n")

	paths, ok := ChangedFiles(cfg, root, DiffSpec{})
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	want := []string{"naïve.go", "small.go", "über/package-lock.json"}
	if got := sortedUnique(paths); !equalStrings(got, want) {
		t.Errorf("ChangedFiles = %q, want %q", got, want)
	}

	diff, ok := GetDiff(cfg, root, DiffSpec{})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if strings.Contains(diff, "+{\"a\": 1}") {
		t.Error("a lock file under a non-ASCII directory is noise and must not spend the budget")
	}
	if !strings.Contains(diff, "excluded") && !strings.Contains(diff, "Excluded") {
		t.Error("the excluded lock file must be named in the preamble")
	}
	if !strings.Contains(diff, "über/package-lock.json") {
		t.Errorf("the preamble must name the excluded file as it is on disk, got %q",
			strings.SplitN(diff, "diff --git ", 2)[0])
	}
	if !strings.Contains(diff, "naïve.go") || strings.Contains(diff, `na\303\257ve.go`) {
		t.Errorf("the budget preamble must name the dropped file as it is on disk, got %q",
			strings.SplitN(diff, "diff --git ", 2)[0])
	}
	if !strings.Contains(diff, "+var b = 2") {
		t.Error("the file that fits the budget must still reach the reviewer")
	}
}

// TestNonASCIIReferencingFiles: grep results compare against the changed list, so a
// quoted name attaches a changed file as an unchanged caller and drops a real caller.
func TestNonASCIIReferencingFiles(t *testing.T) {
	cfg := Defaults()
	root := quotingRepo(t, map[string]string{
		"café.go": "func Fetch(id int) {}\n",
		"über.go": "func run() { Fetch(1) }\n",
	})
	gitCmd(t, root, verb, "-qm", "init")

	hits := ReferencingFiles(cfg, root, map[string]bool{"Fetch": true}, []string{"café.go"})
	if !equalStrings(hits, []string{"über.go"}) {
		t.Errorf("ReferencingFiles = %q, want only the unchanged caller, unquoted", hits)
	}
}

// TestNewlinePathKeepsTheBudgetZip: the name list carries git's verbatim paths to
// the budget splitter, so a newline inside a name must not count as a second path.
// A miscount fails closed to stat-only and hides hunks that fit.
func TestNewlinePathKeepsTheBudgetZip(t *testing.T) {
	cfg := Defaults()
	root := quotingRepo(t, map[string]string{"a\nb.go": "package x\n", "small.go": "package x\n"})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "a\nb.go", "package x\n"+strings.Repeat("var a = 1\n", cfg.MaxDiffChars/8))
	writeFileAt(t, root, "small.go", "package x\n\nvar b = 2\n")

	diff, ok := GetDiff(cfg, root, DiffSpec{})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if strings.Contains(diff, "too large to inline") || !strings.Contains(diff, "+var b = 2") {
		t.Errorf("the file that fits the budget must still reach the reviewer, got %q",
			strings.SplitN(diff, "diff --git ", 2)[0])
	}
}
