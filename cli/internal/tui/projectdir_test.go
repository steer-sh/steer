package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestCodexProjectToggleWritesTheRepositoryRoot pins that arming the Codex project
// gate from a subdirectory writes the file `steer commit-gate install` writes, at
// the repository root, and that disarming removes it there. A write at the working
// directory leaves a stray hooks.json Codex never loads for the repository.
func TestCodexProjectToggleWritesTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	m := New(gate.Defaults(), "global")
	row := channelRow{kind: "commit-gate", scope: claude.ProjectScope,
		armed: status.ScopeArming{Harness: "codex", Scope: claude.ProjectScope}}

	if err := m.toggleChannel(row); err != nil {
		t.Fatal(err)
	}
	if !codex.CommitGateArming(claude.ProjectScope, root).Registered {
		t.Fatal("arming from a subdirectory left the repository root unarmed")
	}
	if _, err := os.Stat(filepath.Join(sub, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("arming wrote %s/.codex (stat err %v)", sub, err)
	}

	row.armed.Registered = true
	if err := m.toggleChannel(row); err != nil {
		t.Fatal(err)
	}
	if codex.CommitGateArming(claude.ProjectScope, root).Registered {
		t.Fatal("disarming from a subdirectory left the repository root armed")
	}
}
