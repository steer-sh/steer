package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

func hostHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("COMMIT_REVIEW_HOST", "")
	return filepath.Join(home, ".steer", "host")
}

// The first read pins the name, so a later hostname change keeps the same shard.
func TestHostIDPinsTheNameItFirstDerives(t *testing.T) {
	path := hostHome(t)
	first := HostID()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != first+"\n" {
		t.Fatalf("host file = %q, %v; want %q", data, err, first)
	}
	if err := os.WriteFile(path, []byte("old-name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HostID(); got != "old-name" {
		t.Errorf("HostID = %q, want the pinned name", got)
	}
}

// The override names another machine in tests and tools, so it pins nothing.
func TestHostOverrideWinsAndWritesNothing(t *testing.T) {
	path := hostHome(t)
	t.Setenv("COMMIT_REVIEW_HOST", "Some.Host.local")
	if got := HostID(); got != "some" {
		t.Errorf("HostID = %q, want some", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the override wrote the host file: %v", err)
	}
}

// A declared former name reads as this machine's; any other name does not.
func TestAddFormerHostDeclaresOnlyAnEarlierName(t *testing.T) {
	path := hostHome(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mini-3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddFormerHost("mini-3"); err == nil {
		t.Error("the current name was accepted as a former one")
	}
	if IsFormerHost("Mini-2") || IsFormerHost("mini-2") {
		t.Error("an undeclared name read as former")
	}
	for range 2 {
		if slug, err := AddFormerHost("Mini-2.local"); err != nil || slug != "mini-2" {
			t.Fatalf("AddFormerHost = %q, %v", slug, err)
		}
	}
	if got := FormerHosts(); len(got) != 1 || got[0] != "mini-2" {
		t.Errorf("FormerHosts = %v, want one mini-2", got)
	}
	if !IsFormerHost("mini-2") || HostID() != "mini-3" {
		t.Errorf("former %v, current %q", IsFormerHost("mini-2"), HostID())
	}
}

// The override names a machine the file does not describe, so the file's former
// names do not apply and a declaration cannot pin it.
func TestHostOverrideIgnoresAndRefusesFormerNames(t *testing.T) {
	path := hostHome(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mini-3\nmini-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMMIT_REVIEW_HOST", "elsewhere")
	if IsFormerHost("mini-2") {
		t.Error("a former name applied under the override")
	}
	if _, err := AddFormerHost("mini-1"); err == nil {
		t.Error("a declaration ran under the override")
	}
	if data, _ := os.ReadFile(path); string(data) != "mini-3\nmini-2\n" {
		t.Errorf("host file changed: %q", data)
	}
}

// Pinning leaves one complete file and no temporary beside it.
func TestPinLeavesOnlyTheHostFile(t *testing.T) {
	path := hostHome(t)
	HostID()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "host" {
		t.Errorf("entries = %v, %v; want only host", entries, err)
	}
}
