package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// codexRemovedHome installs a global guard in Claude Code and Codex, arms Claude
// Code's commit gate, then runs `steer uninstall --harness codex --apply`.
func codexRemovedHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(home)
	g := claude.GlobalScope
	if _, err := claude.InstallManagedHook(g, claude.InstallManagedHookInput{
		HookName: "no-var-hook", FileName: "no-var-hook.py", Rule: "never use var",
		Content: []byte(guardSource), Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}, claude.Registrar{}, codexhooks.Registrar{}); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	harnessFlag, allHarnesses = "codex", false
	t.Cleanup(func() { harnessFlag, allHarnesses = "", false })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	harnessFlag = ""
	t.Chdir(t.TempDir())
}

// assertRebuildSparesCodex fails unless line names the global rebuild for Claude
// Code alone; a plain rebuild would register the removed Codex again.
func assertRebuildSparesCodex(t *testing.T, what, line string) {
	t.Helper()
	if !strings.Contains(line, "steer hooks rebuild --scope global --harness claude-code") || strings.Contains(line, "codex") {
		t.Errorf("%s = %q, want the rebuild for Claude Code alone", what, line)
	}
}

// The fixture run's source-drift advice and verify's missing-master error name
// the rebuild targeted like every other repair.
func TestPrintedRebuildsSpareARemovedHarness(t *testing.T) {
	codexRemovedHome(t)
	g := claude.GlobalScope
	dir, err := hookstore.Dir(g)
	if err != nil {
		t.Fatal(err)
	}
	guardFile := filepath.Join(dir, "no-var-hook.py")
	if err := os.WriteFile(guardFile, []byte(guardSource+"\n# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := fixture.Report{Hook: "no-var-hook", Cases: []fixture.CaseResult{{MasterState: fixture.MasterBlockedAllow}}}
	assertRebuildSparesCodex(t, "the fixture source-drift advice", blockedAllowRepair(report, g))

	master := filepath.Join(dir, hookstore.MasterName)
	if err := os.Remove(master); err != nil {
		t.Fatal(err)
	}
	assertRebuildSparesCodex(t, "verify's missing-master error", missingMasterError(g, master).Error())
}

// A guard command naming no harness registers only where Steer is still
// registered, so `steer register` after a one-harness uninstall leaves Codex out;
// --harness codex is how it is added back.
func TestAGuardCommandRegistersNoRemovedHarness(t *testing.T) {
	codexRemovedHome(t)
	g := claude.GlobalScope
	targets, err := currentRegistrars(g)
	if err != nil {
		t.Fatal(err)
	}
	if names := harnessNames(targets); names != "Claude Code" {
		t.Fatalf("targets after uninstalling Codex = %s, want Claude Code", names)
	}
	if _, err := claude.InstallManagedHook(g, claude.InstallManagedHookInput{
		HookName: "no-let-hook", FileName: "no-let-hook.py", Rule: "never use let",
		Content: []byte(guardSource), Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}, targets...); err != nil {
		t.Fatal(err)
	}
	if path, err := (codexhooks.Registrar{}).RegisteredMasterPath(g); err != nil || path != "" {
		t.Errorf("a new guard registered the runner in Codex again: %q (%v)", path, err)
	}

	harnessFlag = "codex"
	if targets, err = currentRegistrars(g); err != nil {
		t.Fatal(err)
	}
	if names := harnessNames(targets); names != "Codex" {
		t.Errorf("--harness codex targets %s, want Codex", names)
	}
}

// Two harnesses that each carry only a guard runner both receive a new guard.
func TestAGuardCommandReachesEveryRegisteredHarness(t *testing.T) {
	home := isolatedHome(t)
	t.Chdir(home)
	g := claude.GlobalScope
	if _, err := claude.InstallManagedHook(g, claude.InstallManagedHookInput{
		HookName: "no-var-hook", FileName: "no-var-hook.py", Rule: "never use var",
		Content: []byte(guardSource), Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}, claude.Registrar{}, codexhooks.Registrar{}); err != nil {
		t.Fatal(err)
	}
	targets, err := currentRegistrars(g)
	if err != nil {
		t.Fatal(err)
	}
	if names := harnessNames(targets); names != "Claude Code, Codex" {
		t.Errorf("guards-only machine targets %s, want Claude Code, Codex", names)
	}
}
