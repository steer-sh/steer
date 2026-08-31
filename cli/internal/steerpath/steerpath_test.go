package steerpath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateDirIsUnderTheEvidenceHome(t *testing.T) {
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
	want := filepath.Join(home, ".claude", "state", "commit-review")
	if got != want {
		t.Errorf("StateDir = %q; want %q", got, want)
	}
}
