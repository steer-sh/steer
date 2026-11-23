package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

// TestWriteFileReplacesContentAndSetsMode checks that a write over an existing
// file leaves the new bytes at the requested mode and no temporary file beside it.
func TestWriteFileReplacesContentAndSetsMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644, 0o755} {
		dir := t.TempDir()
		path := filepath.Join(dir, "target")
		if err := os.WriteFile(path, []byte("old content that is longer"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(path, []byte("new"), mode); err != nil {
			t.Fatalf("WriteFile(%v): %v", mode, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new" {
			t.Errorf("mode %v: content = %q, want %q", mode, got, "new")
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != mode {
			t.Errorf("mode = %v, want %v", info.Mode().Perm(), mode)
		}
		if names := entries(t, dir); len(names) != 1 {
			t.Errorf("mode %v: directory holds %v, want only target", mode, names)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// TestFailedWriteLeavesTargetAndRemovesTemp checks both failure points: a stream
// that errors before the rename, and a rename refused because the target is a
// non-empty directory. Each returns the error, keeps the target and removes the
// temporary file.
func TestFailedWriteLeavesTargetAndRemovesTemp(t *testing.T) {
	t.Run("stream", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "target")
		if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Copy(path, failingReader{}, 0o644); err == nil {
			t.Fatal("Copy returned nil for a failing reader")
		}
		if got, _ := os.ReadFile(path); string(got) != "keep" {
			t.Errorf("target = %q, want it unchanged", got)
		}
		if names := entries(t, dir); len(names) != 1 {
			t.Errorf("directory holds %v, want only target", names)
		}
	})
	t.Run("rename", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "target")
		if err := os.MkdirAll(filepath.Join(path, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(path, []byte("new"), 0o644); err == nil {
			t.Fatal("WriteFile returned nil over a non-empty directory")
		}
		if names := entries(t, dir); len(names) != 1 {
			t.Errorf("directory holds %v, want only target", names)
		}
	})
}

// TestWriteFileTakesAMaximalName checks that a destination whose name is at the
// 255-byte component limit is written: the temporary name must not grow with it.
func TestWriteFileTakesAMaximalName(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("n", 255))
	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile with a 255-byte name: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "x" {
		t.Errorf("content = %q, want %q", got, "x")
	}
}
