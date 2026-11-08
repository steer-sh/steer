package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// twoHarnessGuardHome installs one global guard in Claude Code and Codex, both
// installed, in a throwaway home where Claude Code also carries the commit gate.
func twoHarnessGuardHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(t.TempDir())
	content := []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	if _, err := claude.InstallManagedHook(claude.GlobalScope, claude.InstallManagedHookInput{
		HookName: "no-bare-python", Rule: "use python3 instead", Content: content,
		Metadata: claude.HookInstallMetadata{Source: claude.SourceGenerated},
	}, claude.Registrar{}, codex.Registrar{}); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterCommitGateAndResolver(claude.GlobalScope, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
}

// removeCodex leaves Codex installed with none of Steer's global registrations,
// the state `steer uninstall --harness codex --apply` leaves.
func removeCodex(t *testing.T) {
	t.Helper()
	g := claude.GlobalScope
	if err := (codex.Registrar{}).RemoveMaster(g); err != nil {
		t.Fatal(err)
	}
	if err := (codex.Registrar{}).SetRules(nil, g); err != nil {
		t.Fatal(err)
	}
}

// assertCodexUnregistered fails when Codex's global configuration runs the guard
// runner again or its instructions carry a rule again.
func assertCodexUnregistered(t *testing.T, after string) {
	t.Helper()
	g := claude.GlobalScope
	if path, err := (codex.Registrar{}).RegisteredMasterPath(g); err != nil || path != "" {
		t.Errorf("after %s, Codex registers the guard runner %q (%v)", after, path, err)
	}
	agents, err := codex.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	if doc, err := os.ReadFile(agents); err == nil && strings.Contains(string(doc), "use python3") {
		t.Errorf("after %s, Codex's instructions carry the rule again:\n%s", after, doc)
	}
}

func dashboardOver(t *testing.T, targets ...harness.Registrar) *Model {
	t.Helper()
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	return New(cfg, claude.GlobalScope, targets...)
}

func firstSecurityPreset(t *testing.T) string {
	t.Helper()
	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	names, err := catalog.BundleMembers(presets.BundleSecurity)
	if err != nil || len(names) == 0 {
		t.Fatalf("no security preset (%v)", err)
	}
	return names[0]
}

// Turning a built-in guard on or off, archiving a guard and restoring it register
// only where Steer is still registered, so a harness Steer was removed from stays
// removed.
func TestAGuardActionRegistersNoRemovedHarness(t *testing.T) {
	twoHarnessGuardHome(t)
	removeCodex(t)
	m := dashboardOver(t, claude.Registrar{}, codex.Registrar{})

	name := firstSecurityPreset(t)
	m.togglePreset(name, false)
	if !strings.Contains(m.message, "is on") {
		t.Fatalf("the built-in guard was not turned on: %q", m.message)
	}
	assertCodexUnregistered(t, "turning a built-in guard on")
	m.togglePreset(name, true)
	assertCodexUnregistered(t, "turning a built-in guard off")

	if _, err := m.archiveFix(archiveForm{tier: "guard", id: "no-bare-python", scope: claude.GlobalScope}, "test"); err != nil {
		t.Fatal(err)
	}
	assertCodexUnregistered(t, "archiving a guard")
	entries, _ := readArchived("")
	var restored bool
	for i := range entries {
		if entries[i].stored.ID == "no-bare-python" {
			m.restoreArchived(&entries[i])
			restored = true
		}
	}
	if !restored || !strings.HasPrefix(m.message, "restored") {
		t.Fatalf("the guard was not restored: %q", m.message)
	}
	assertCodexUnregistered(t, "restoring a guard")
}

// An uninstall run in another terminal after the dashboard read its snapshot is
// seen by the next repair: the targets are read when the action runs.
func TestARepairReadsRegistrationsWhenItRuns(t *testing.T) {
	twoHarnessGuardHome(t)
	m := dashboardOver(t, claude.Registrar{}, codex.Registrar{})
	opened := &status.SystemStatus{Posture: status.Posture{
		Masters: status.MasterRuntimes(claude.GlobalScope), Rules: status.InstalledRules(claude.GlobalScope)}}
	removeCodex(t)
	for _, action := range []struct {
		name, done string
		run        func()
	}{{"rebuild", "the runner now carries", m.rebuildMaster}, {"sync", "synced 0 agent(s); 1 already matched", m.syncStore}} {
		m.snap, m.message = opened, ""
		action.run()
		if !strings.HasPrefix(m.message, action.done) {
			t.Fatalf("%s did not run in Claude Code alone: %q", action.name, m.message)
		}
		assertCodexUnregistered(t, "a "+action.name+" from a snapshot taken before the uninstall")
	}
}

// A harness the user names with a flag is written to even while it holds nothing:
// that is how Steer is added back to a harness it was removed from.
func TestANamedHarnessIsRegisteredAgain(t *testing.T) {
	twoHarnessGuardHome(t)
	removeCodex(t)
	m := dashboardOver(t, codex.Registrar{})
	m.named = true
	m.rebuildMaster()
	if path, err := (codex.Registrar{}).RegisteredMasterPath(claude.GlobalScope); err != nil || path == "" {
		t.Errorf("a rebuild named for Codex left it unregistered (%v): %q", err, m.message)
	}
}
