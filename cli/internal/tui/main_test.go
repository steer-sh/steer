package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain points every Steer root at an empty directory. The dashboard reads the
// evidence corpora and the harness config trees on construction, and those roots
// resolve from the home directory, so without this the package walks the operator's
// real transcripts once per test.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "steer-tui-home")
	if err != nil {
		panic(err)
	}
	// os.UserHomeDir reads USERPROFILE on Windows and HOME everywhere else.
	if runtime.GOOS == "windows" {
		os.Setenv("USERPROFILE", home)
	} else {
		os.Setenv("HOME", home)
	}
	os.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	os.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	os.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(home, ".pi", "agent", "sessions"))
	// An operator's own overrides would point back out of the empty home.
	for _, key := range []string{"COMMIT_GATE_STATE_DIR", "COMMIT_REVIEW_STATE"} {
		os.Unsetenv(key)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
