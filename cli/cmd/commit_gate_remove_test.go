package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestCommitGateRemoveNeedsATarget: a removal with a flag missing changes nothing.
func TestCommitGateRemoveNeedsATarget(t *testing.T) {
	dir := t.TempDir()
	testhome.Set(t, dir)
	t.Chdir(dir)
	settings := filepath.Join(dir, ".claude", "settings.json")
	if err := claude.RegisterCommitGateAndResolver(claude.GlobalScope, "steer commit-gate review", 460, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	cmd := commitGateRemoveCmd
	commitGateRemoveHarness = "claude-code"
	t.Cleanup(func() { commitGateRemoveHarness = "" })
	err = cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--scope") || !strings.Contains(err.Error(), "claude-code global") {
		t.Fatalf("err = %v, want a refusal naming --scope and the registered gate", err)
	}
	after, _ := os.ReadFile(settings)
	if string(before) != string(after) {
		t.Error("a refused removal rewrote the settings file")
	}
}

// TestCommitGateRemoveResultSaysNothingChanged: "Removed" prints only for a removal.
func TestCommitGateRemoveResultSaysNothingChanged(t *testing.T) {
	if got := commitGateRemoveResult("Codex", "project", "/p", false); strings.Contains(got, "Removed") || !strings.Contains(got, "nothing changed") {
		t.Errorf("no-op result = %q", got)
	}
	if got := commitGateRemoveResult("Codex", "project", "/p", true); !strings.Contains(got, "Removed Codex commit review hooks from /p") {
		t.Errorf("removal result = %q", got)
	}
}

// TestRemoveFlagLeavesInstallDefault: the two commands keep separate harness values.
func TestRemoveFlagLeavesInstallDefault(t *testing.T) {
	if commitGateHarness != "claude-code" {
		t.Errorf("install's default harness = %q", commitGateHarness)
	}
}
