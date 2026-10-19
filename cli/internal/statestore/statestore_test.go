package statestore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const component = "commit-review"

// tempHome points the home directory at a fresh temporary directory, which both
// roots resolve against.
func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestResolveAnswersTheNeutralRootOnAnEmptyHome(t *testing.T) {
	tempHome(t)
	if got, want := Resolve(component), steerpath.StateDir(component); got != want {
		t.Errorf("Resolve = %q; want %q", got, want)
	}
}

func TestResolveAnswersTheLegacyRootUntilTheCorpusMoves(t *testing.T) {
	tempHome(t)
	legacy := steerpath.LegacyStateDir(component)
	write(t, filepath.Join(legacy, "adjudication.host.jsonl"), "{}\n")
	if got := Resolve(component); got != legacy {
		t.Errorf("Resolve = %q; want the legacy root %q", got, legacy)
	}
	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	if got, want := Resolve(component), steerpath.StateDir(component); got != want {
		t.Errorf("after Prepare, Resolve = %q; want %q", got, want)
	}
}

func TestPrepareCopiesTheWholeCorpusAndLeavesTheLegacyFilesInPlace(t *testing.T) {
	tempHome(t)
	legacy := steerpath.LegacyStateDir(component)
	write(t, filepath.Join(legacy, "adjudication.host.jsonl"), "row\n")
	write(t, filepath.Join(legacy, "diffs", "1-a-r1.diff"), "diff\n")
	write(t, filepath.Join(legacy, "tasks", "t.jsonl"), "task\n")

	neutral, err := Prepare(component)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"adjudication.host.jsonl": "row\n",
		"diffs/1-a-r1.diff":       "diff\n",
		"tasks/t.jsonl":           "task\n",
	} {
		if got := read(t, filepath.Join(neutral, filepath.FromSlash(name))); got != want {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
		if _, err := os.Stat(filepath.Join(legacy, filepath.FromSlash(name))); err != nil {
			t.Errorf("legacy %s: %v", name, err)
		}
	}
}

func TestPrepareNeverOverwritesTheNeutralCopy(t *testing.T) {
	tempHome(t)
	legacy := steerpath.LegacyStateDir(component)
	neutral := steerpath.StateDir(component)
	write(t, filepath.Join(legacy, "adjudication.host.jsonl"), "old\n")
	write(t, filepath.Join(neutral, "adjudication.host.jsonl"), "new\n")

	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(neutral, "adjudication.host.jsonl")); got != "new\n" {
		t.Errorf("ledger = %q; want the neutral copy %q", got, "new\n")
	}
}

// An interrupted copy writes files but never the marker, so the store still
// reads legacy and the next Prepare finishes what is missing.
func TestAnInterruptedCopyIsRetriedWhole(t *testing.T) {
	tempHome(t)
	legacy := steerpath.LegacyStateDir(component)
	neutral := steerpath.StateDir(component)
	write(t, filepath.Join(legacy, "a.jsonl"), "a\n")
	write(t, filepath.Join(legacy, "b.jsonl"), "b\n")
	write(t, filepath.Join(neutral, "a.jsonl"), "a\n")

	if got := Resolve(component); got != legacy {
		t.Fatalf("an unmarked neutral root resolved to %q; want the legacy root", got)
	}
	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(neutral, "b.jsonl")); got != "b\n" {
		t.Errorf("b.jsonl = %q; want %q", got, "b\n")
	}
}

func TestPrepareIsIdempotentAndDoesNotReReadTheLegacyRoot(t *testing.T) {
	tempHome(t)
	legacy := steerpath.LegacyStateDir(component)
	neutral := steerpath.StateDir(component)
	write(t, filepath.Join(legacy, "a.jsonl"), "a\n")
	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	// A legacy file added after the migration is another tool's business: the
	// corpus has moved and every writer now names the neutral root.
	write(t, filepath.Join(legacy, "late.jsonl"), "late\n")
	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(neutral, "late.jsonl")); !os.IsNotExist(err) {
		t.Errorf("late.jsonl was copied after the migration marker landed")
	}
}

func TestPrepareMarksAFreshHomeWithNothingToMove(t *testing.T) {
	tempHome(t)
	if _, err := Prepare(component); err != nil {
		t.Fatal(err)
	}
	if !Migrated(component) {
		t.Error("a fresh home was not marked migrated")
	}
}

func TestPrepareAllMovesEveryComponent(t *testing.T) {
	tempHome(t)
	for _, c := range Components {
		write(t, filepath.Join(steerpath.LegacyStateDir(c), "shard.jsonl"), c+"\n")
	}
	if err := PrepareAll(); err != nil {
		t.Fatal(err)
	}
	for _, c := range Components {
		if got := read(t, filepath.Join(steerpath.StateDir(c), "shard.jsonl")); got != c+"\n" {
			t.Errorf("%s shard = %q; want %q", c, got, c+"\n")
		}
	}
}

// The evidence root is Steer's alone, but the legacy root is not: another tool's
// state directory sits beside the corpora and must not move.
func TestMigrationTouchesOnlySteersOwnComponents(t *testing.T) {
	home := tempHome(t)
	write(t, filepath.Join(steerpath.LegacyStateDir(component), "a.jsonl"), "a\n")
	other := filepath.Join(home, ".claude", "state", "plan-review")
	write(t, filepath.Join(other, "notes.json"), "{}\n")

	if err := PrepareAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".steer", "state", "plan-review")); !os.IsNotExist(err) {
		t.Error("another tool's state directory was migrated")
	}
	if got := read(t, filepath.Join(other, "notes.json")); got != "{}\n" {
		t.Errorf("another tool's file changed: %q", got)
	}
}
