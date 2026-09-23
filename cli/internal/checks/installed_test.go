package checks

import (
	"os"
	"path/filepath"
	"testing"
)

// Each declared check maps to its detector now; a check whose detector no
// longer reads stays declared with no digest, and a repository with no manifest
// declares nothing.
func TestInstalledDetectorsReadsEachDeclaredCheck(t *testing.T) {
	root := execRepo(t, map[string]string{"a": "exit 0\n", "b": "exit 0\n"})
	got, err := InstalledDetectors(root)
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] == "" || got["b"] == "" || got["a"] == got["b"] {
		t.Fatalf("detectors = %v; want a distinct digest for a and b", got)
	}
	if err := os.Remove(filepath.Join(root, ".steer/checks/b.sh")); err != nil {
		t.Fatal(err)
	}
	got, err = InstalledDetectors(root)
	if err != nil {
		t.Fatal(err)
	}
	if digest, declared := got["b"]; !declared || digest != "" || got["a"] == "" {
		t.Errorf("detectors = %v; want b declared without a digest and a unchanged", got)
	}

	bare, err := InstalledDetectors(t.TempDir())
	if err != nil || bare == nil || len(bare) != 0 {
		t.Errorf("a repository with no manifest = %v, %v; want an empty map", bare, err)
	}
}
