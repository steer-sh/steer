package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	return home
}

func TestLoadMissingFileReturnsNoOverrides(t *testing.T) {
	useTempHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != (Config{}) {
		t.Fatalf("Load missing file = %#v; want no overrides", cfg)
	}
}

func TestSaveAndLoadReviewerSettings(t *testing.T) {
	useTempHome(t)
	want := Config{Reviewer: Reviewer{
		Provider: "codex",
		Model:    "gpt-reviewer",
		Effort:   "high",
		Runs:     5,
		Timeout:  210,
	}}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %#v; want %#v", got, want)
	}
	info, err := os.Stat(steerpath.ConfigFile())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("config permissions = %o; want 644", info.Mode().Perm())
	}
}

func TestLoadReportsInvalidJSON(t *testing.T) {
	home := useTempHome(t)
	dir := filepath.Join(home, ".steer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load invalid JSON succeeded; want an error")
	}
}
