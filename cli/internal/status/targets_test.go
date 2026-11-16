package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

func targetIDs(targets []harness.Registrar, err error) []string {
	if err != nil {
		return []string{"error: " + err.Error()}
	}
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, string(t.Harness().ID))
	}
	return ids
}

// A fresh machine's first guard reaches every installed harness; once one holds a
// global Steer registration, a harness holding none is left alone, and the
// project scope keeps every candidate. A rules section counts, so removing a rule
// reaches every harness that carries one, unless another harness reads the same
// document, which a one-harness uninstall keeps.
func TestGuardTargetsFollowTheGlobalRegistrations(t *testing.T) {
	home := projectDir(t)
	testhome.Set(t, home)
	codexHome := filepath.Join(home, ".codex")
	for _, dir := range []string{codexHome, filepath.Join(home, ".claude")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(t.TempDir())
	g, p := harness.GlobalScope, harness.ProjectScope
	both := []harness.Registrar{claude.Registrar{}, codex.Registrar{}}

	if got := targetIDs(GuardTargets(g, both)); len(got) != 2 {
		t.Fatalf("fresh machine: GuardTargets = %q, want both harnesses", got)
	}
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if got := targetIDs(GuardTargets(g, both)); len(got) != 1 || got[0] != "claude-code" {
		t.Errorf("Claude Code's commit gate alone: GuardTargets = %q, want claude-code", got)
	}
	if got := targetIDs(GuardTargets(p, both)); len(got) != 2 {
		t.Errorf("project scope: GuardTargets = %q, want both harnesses", got)
	}
	if err := (codex.Registrar{}).SetRules([]string{"use python3 instead"}, g); err != nil {
		t.Fatal(err)
	}
	if got := targetIDs(GuardTargets(g, both)); len(got) != 2 {
		t.Errorf("a Codex rules section: GuardTargets = %q, want both harnesses", got)
	}
	agents, err := codex.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	claudeMD, err := claude.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(agents, claudeMD); err != nil {
		t.Fatal(err)
	}
	if got := targetIDs(GuardTargets(g, both)); len(got) != 1 || got[0] != "claude-code" {
		t.Errorf("a rules section Codex shares with Claude Code: GuardTargets = %q, want claude-code", got)
	}
	if err := (codex.Registrar{}).SetRules(nil, g); err != nil {
		t.Fatal(err)
	}
	if err := (codex.Registrar{}).RegisterMaster(g); err != nil {
		t.Fatal(err)
	}
	if got := targetIDs(GuardTargets(g, both)); len(got) != 2 {
		t.Errorf("a Codex guard runner: GuardTargets = %q, want both harnesses", got)
	}
}

// A drifted rules document that a removed Codex shares with a registered Claude
// Code is read on Claude Code's row alone, so its repair never names Codex.
func TestASharedRulesDocumentNamesNoRemovedHarness(t *testing.T) {
	home := projectDir(t)
	testhome.Set(t, home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(t.TempDir())
	g := harness.GlobalScope
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if err := (claude.Registrar{}).SetRules([]string{"a rule no manifest names"}, g); err != nil {
		t.Fatal(err)
	}
	claudeMD, err := claude.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := codex.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeMD, agents); err != nil {
		t.Fatal(err)
	}

	rules := InstalledRules(g)
	for _, r := range rules {
		if r.Harness == "codex" {
			t.Errorf("the shared document reads on a Codex row: %#v", r)
		}
	}
	got := RulesRepair(rules, MasterRuntimes(g), g)
	if want := SyncCommand(g) + " --harness claude-code"; got != want {
		t.Errorf("RulesRepair = %q, want %q", got, want)
	}

	// With no registration anywhere, neither row defers to the other.
	if _, err := claude.RemoveCommitGateAndResolver(g); err != nil {
		t.Fatal(err)
	}
	drifted := 0
	for _, r := range InstalledRules(g) {
		if r.State == RulesDrifted {
			drifted++
		}
	}
	if drifted != 2 {
		t.Errorf("an unregistered shared document drifted on %d rows, want both", drifted)
	}
}

// A harness whose hooks file cannot be read is not taken for one Steer was removed
// from: the selection fails rather than leave it out.
func TestGuardTargetsRefuseAnUnreadableHarness(t *testing.T) {
	home := projectDir(t)
	testhome.Set(t, home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(t.TempDir())
	g := harness.GlobalScope
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	both := []harness.Registrar{claude.Registrar{}, codex.Registrar{}}
	if got, err := GuardTargets(g, both); err == nil {
		t.Errorf("GuardTargets with an unreadable Codex hooks file = %q, want an error", targetIDs(got, nil))
	}
	// A first guard fails before it writes anything, not part way through.
	if _, err := claude.RemoveCommitGateAndResolver(g); err != nil {
		t.Fatal(err)
	}
	if got, err := GuardTargets(g, both); err == nil {
		t.Errorf("GuardTargets on a fresh machine with an unreadable Codex hooks file = %q, want an error", targetIDs(got, nil))
	}
}

// An instruction document that cannot be read is not taken for one without rules.
func TestGuardTargetsRefuseAnUnreadableInstructionDocument(t *testing.T) {
	home := projectDir(t)
	testhome.Set(t, home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "no-pi"))
	t.Chdir(t.TempDir())
	g := harness.GlobalScope
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	agents, err := codex.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	both := []harness.Registrar{claude.Registrar{}, codex.Registrar{}}
	if got, err := GuardTargets(g, both); err == nil {
		t.Errorf("GuardTargets with an unreadable AGENTS.md = %q, want an error", targetIDs(got, nil))
	}
}
