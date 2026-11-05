package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// runCheckCmd runs a check subcommand at global scope in a fake home and
// returns its output.
func runCheckCmd(t *testing.T, c *cobra.Command, harnessName string) (string, error) {
	t.Helper()
	old := scope
	scope = claude.GlobalScope
	t.Cleanup(func() { scope = old })
	if err := c.Flags().Set("harness", harnessName); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c.SetOut(&out)
	t.Cleanup(func() { c.SetOut(nil) })
	err := c.RunE(c, nil)
	return out.String(), err
}

func fakeHomes(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	withCmdProjectTempDir(t)
	return home
}

func TestCheckInstallArmsRootStopInBothHarnesses(t *testing.T) {
	home := fakeHomes(t)

	for _, target := range runnerHarnesses {
		out, err := runCheckCmd(t, checkInstallCmd, string(target))
		if err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
		a, err := checkRunnerArming(target, claude.GlobalScope)
		if err != nil {
			t.Fatal(err)
		}
		if !a.Registered || a.Timeout != checks.RegisteredHookTimeout() {
			t.Fatalf("%s arming = %+v, want registered with timeout %d", target, a, checks.RegisteredHookTimeout())
		}
		if !strings.HasSuffix(a.Command, "check hook --harness "+string(target)) ||
			steerbin.Resolve(a.Command) != steerbin.Resolvable {
			t.Fatalf("%s command = %q, want a resolvable stable-path check hook", target, a.Command)
		}
		if !strings.Contains(out, a.Path) {
			t.Fatalf("%s install output does not name %s:\n%s", target, a.Path, out)
		}
		if target == harness.Codex && !strings.Contains(out, "/hooks") {
			t.Fatalf("the Codex install does not name the trust review:\n%s", out)
		}
	}
	if checks.RegisteredHookTimeout() <= int(checks.DefaultExecTimeout.Seconds()) {
		t.Fatal("the registered timeout does not exceed the run deadline")
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SubagentStop") {
		t.Fatalf("install wrote a SubagentStop entry:\n%s", raw)
	}

	var wiring bytes.Buffer
	printRunnerWiring(&wiring)
	if got := wiring.String(); !strings.Contains(got, "Claude Code, global scope") || !strings.Contains(got, "Codex, global scope") {
		t.Fatalf("runner wiring = %q, want both global registrations", got)
	}
}

func TestInstallNamesAdoptionOnlyWhereCheckShExists(t *testing.T) {
	fakeHomes(t)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	hasAdoptStep := func() bool {
		for _, step := range checkInstallSteps(harness.ClaudeCode, checkscope.Arming{}) {
			if step.Fix == "steer check adopt" {
				return true
			}
		}
		return false
	}

	if hasAdoptStep() {
		t.Fatal("a repository with no check.sh was told to adopt")
	}
	if err := os.WriteFile(checks.OracleName, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasAdoptStep() {
		t.Fatal("an unadopted repository with a check.sh was not told to adopt")
	}
}

func TestCheckInstallRefusesPi(t *testing.T) {
	fakeHomes(t)

	if _, err := runCheckCmd(t, checkInstallCmd, "pi"); err == nil {
		t.Fatal("install accepted Pi, which has no blocking Stop support")
	}
}

func TestCheckUninstallKeepsTheResolver(t *testing.T) {
	fakeHomes(t)
	if err := claude.RegisterBackfillHook(claude.GlobalScope, "steer commit-gate backfill", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := runCheckCmd(t, checkInstallCmd, "claude-code"); err != nil {
		t.Fatal(err)
	}

	if _, err := runCheckCmd(t, checkUninstallCmd, "claude-code"); err != nil {
		t.Fatal(err)
	}
	if a, _ := claude.CheckHookArming(claude.GlobalScope); a.Registered {
		t.Fatal("uninstall left the runner registered")
	}
	if !claude.BackfillHookArming(claude.GlobalScope).Registered {
		t.Fatal("uninstall removed the resolver")
	}
	out, err := runCheckCmd(t, checkUninstallCmd, "claude-code")
	if err != nil || !strings.Contains(out, "No Claude Code Stop runner is registered") {
		t.Fatalf("a second uninstall = %q, %v", out, err)
	}
}

func TestBinInstallRelinksTheCheckRunner(t *testing.T) {
	fakeHomes(t)
	if err := claude.RegisterCheckHook(claude.GlobalScope, "steer check hook --harness claude-code", 330); err != nil {
		t.Fatal(err)
	}
	if _, err := codexhooks.RegisterCheckHook(claude.GlobalScope, "", "steer check hook --harness codex", 330); err != nil {
		t.Fatal(err)
	}
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}

	rewritten, err := repointRegistrations(path)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != 2 {
		t.Fatalf("rewrote %d registration(s), want both runners", rewritten)
	}
	for _, target := range runnerHarnesses {
		a, err := checkRunnerArming(target, claude.GlobalScope)
		if err != nil {
			t.Fatal(err)
		}
		want := steerbin.Quote(path) + " check hook --harness " + string(target)
		if a.Command != want || a.Timeout != 330 {
			t.Fatalf("%s runner = %q timeout %d, want %q timeout 330", target, a.Command, a.Timeout, want)
		}
	}
}

func TestRunnerWiringNamesAShortTimeout(t *testing.T) {
	fakeHomes(t)
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterCheckHook(claude.GlobalScope, steerbin.Quote(path)+" check hook --harness claude-code", 60); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printRunnerWiring(&out)
	if !strings.Contains(out.String(), "timeout 60s is shorter than a run") {
		t.Fatalf("runner wiring = %q, want the short timeout named", out.String())
	}
}
