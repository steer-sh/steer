package checks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The approval list is read in file order without comments, blanks or repeats,
// a leading ~ names the home the list lives in, and a missing list approves
// nothing.
func TestPersonalApprovalsReadsTheGateList(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	path, err := PersonalApprovalsPath()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PersonalApprovals(); err != nil || len(got) != 0 {
		t.Fatalf("PersonalApprovals with no list = %v, %v; want none", got, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# a comment\n\n  /src/one  \n~/two\n/src/one\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := PersonalApprovals()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/src/one", filepath.Join(home, "two")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PersonalApprovals = %v; want %v", got, want)
	}
}

// The personal gate is recognised by its script name, wherever a harness
// wraps it, and the Steer runner is not mistaken for it.
func TestIsPersonalGateCommandNamesTheScript(t *testing.T) {
	if !IsPersonalGateCommand("env CHECK_GATE_STATE_DIR=~/.codex/state python3 ~/.claude/hooks/check-gate.py") {
		t.Fatal("a wrapped personal gate was not recognised")
	}
	if IsPersonalGateCommand("/Users/someone/.steer/bin/steer check hook --harness codex") {
		t.Fatal("the Steer runner was read as the personal gate")
	}
}
