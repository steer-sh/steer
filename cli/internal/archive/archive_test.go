package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteStoresTheEntryAndItsFiles(t *testing.T) {
	store := t.TempDir()
	dir, err := Write(store, Entry{Tier: "guard", ID: "alpha", Scope: "project", Reason: "too broad"},
		map[string][]byte{"alpha.py": []byte("print('a')\n")})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != filepath.Join(store, DirName, "alpha") {
		t.Fatalf("entry written to %s", dir)
	}

	got, err := List(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Version != Version || e.Tier != "guard" || e.ID != "alpha" || e.Reason != "too broad" || e.Removed == "" {
		t.Fatalf("entry read back as %+v", e.Entry)
	}
	if len(e.Files) != 1 || e.Files[0] != "alpha.py" {
		t.Fatalf("files = %v", e.Files)
	}
	content, err := os.ReadFile(filepath.Join(e.Dir, "alpha.py"))
	if err != nil || string(content) != "print('a')\n" {
		t.Fatalf("archived file = %q, %v", content, err)
	}
}

func TestTwoRemovalsOfOneIDKeepTwoEntries(t *testing.T) {
	store := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := Write(store, Entry{Tier: "guard", ID: "alpha", Scope: "project"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Dir == got[1].Dir {
		t.Fatalf("two writes left %d entries", len(got))
	}
}

func TestListOrdersByTimeNotByText(t *testing.T) {
	store := t.TempDir()
	// Listed in text order, .5Z sorts after .51Z although it is earlier.
	for name, removed := range map[string]string{
		"b": "2026-09-14T12:00:00.5Z",
		"a": "2026-09-14T12:00:00.51Z",
	} {
		dir := filepath.Join(store, DirName, "alpha", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := `{"version":1,"tier":"guard","id":"alpha","scope":"project","removed":"` + removed + `"}`
		if err := os.WriteFile(filepath.Join(dir, EntryName), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Removed != "2026-09-14T12:00:00.5Z" {
		t.Fatalf("listed out of time order: %+v", got)
	}
}

func TestAnInterruptedWriteReadsAsNoEntry(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, DirName, "alpha", tempPrefix+"123"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := List(store)
	if err != nil {
		t.Fatalf("a leftover temporary entry broke the listing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a leftover temporary entry read as %d entries", len(got))
	}
}

func stored(kind, id, name string) Stored {
	return Stored{Entry: Entry{Tier: kind, ID: id}, Dir: filepath.Join("archive", id, name)}
}

func TestSelectNeedsATierWhenOneIDHoldsTwo(t *testing.T) {
	entries := []Stored{stored("guard", "alpha", "e1"), stored("instruction", "alpha", "e2"), stored("guard", "beta", "e3")}
	if _, err := Select(entries, "alpha", "", ""); err == nil || !strings.Contains(err.Error(), "--tier") {
		t.Fatalf("an id held by two tiers was selected without --tier: %v", err)
	}
	got, err := Select(entries, "alpha", "instruction", "")
	if err != nil || got.Name() != "e2" {
		t.Fatalf("--tier instruction selected %q, %v", got.Name(), err)
	}
	if _, err := Select(entries, "alpha", "check", ""); err == nil || !strings.Contains(err.Error(), "guard, instruction") {
		t.Fatalf("a tier with no entry was not refused with the tiers it has: %v", err)
	}
}

func TestSelectNeedsAnEntryWhenOneTierHoldsTwo(t *testing.T) {
	entries := []Stored{stored("guard", "alpha", "e1"), stored("guard", "alpha", "e2")}
	if _, err := Select(entries, "alpha", "guard", ""); err == nil || !strings.Contains(err.Error(), "--entry with one of: e1, e2") {
		t.Fatalf("two entries of one tier were not refused: %v", err)
	}
	got, err := Select(entries, "alpha", "", "e2")
	if err != nil || got.Name() != "e2" {
		t.Fatalf("--entry e2 selected %q, %v", got.Name(), err)
	}
	if _, err := Select(entries, "alpha", "", "e9"); err == nil {
		t.Fatal("an entry name that is not in the archive was selected")
	}
	if _, err := Select(entries, "gamma", "", ""); err == nil {
		t.Fatal("an id with no entries was selected")
	}
}

func TestWriteRefusesANameThatLeavesTheEntry(t *testing.T) {
	store := t.TempDir()
	for _, id := range []string{"", "..", "a/b", ".hidden"} {
		if _, err := Write(store, Entry{ID: id}, nil); err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
	if _, err := Write(store, Entry{ID: "alpha"}, map[string][]byte{"../escape.py": nil}); err == nil {
		t.Error("a file name that leaves the entry was accepted")
	}
	if _, err := Write(store, Entry{ID: "alpha"}, map[string][]byte{EntryName: nil}); err == nil {
		t.Errorf("a file named %s was accepted", EntryName)
	}
	if _, err := os.Stat(filepath.Join(store, "escape.py")); !os.IsNotExist(err) {
		t.Fatal("a refused write left a file behind")
	}
}
