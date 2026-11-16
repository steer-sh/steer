package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// TestCodexProjectPostureReadsTheRepositoryRoot pins that status run from a
// subdirectory reads the project registration `steer commit-gate install` wrote at
// the repository root. Read at the working directory, the gate reads as unarmed
// and status tells the operator their commits go unreviewed.
func TestCodexProjectPostureReadsTheRepositoryRoot(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := codex.RegisterCommitGateAndResolver(claude.ProjectScope, root,
		"steer commit-gate review-codex", 210,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	p := collectPosture(gate.Config{}, mine.EvidenceSet{})

	for _, rows := range [][]ScopeArming{p.CommitGate, p.Resolver} {
		found := false
		for _, a := range rows {
			if a.Harness == "codex" && a.Scope == claude.ProjectScope {
				found = true
				if !a.Registered {
					t.Errorf("codex project row from a subdirectory = %+v, want registered", a)
				}
			}
		}
		if !found {
			t.Error("no codex project row")
		}
	}
}
