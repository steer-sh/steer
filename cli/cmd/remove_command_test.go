package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// TestRemoveCommandResolvesForEveryTier runs each tier's remove command, the one
// the dashboard and the status phrases print, through the real command tree: it
// must reach a remove command with the fix's ID as its only argument and select
// the scope it names.
func TestRemoveCommandResolvesForEveryTier(t *testing.T) {
	t.Cleanup(func() {
		scope = claude.ProjectScope
		rootCmd.PersistentFlags().Lookup("scope").Changed = false
	})
	cases := []struct {
		tier  tier.Kind
		scope string
		path  string
	}{
		{tier.Guard, claude.ProjectScope, "steer remove"},
		{tier.Guard, claude.GlobalScope, "steer remove"},
		{tier.Instruction, claude.GlobalScope, "steer rules remove"},
		{tier.Review, claude.GlobalScope, "steer commit-gate addendum remove"},
		{tier.Check, "", "steer check remove"},
	}
	for _, c := range cases {
		line := status.RemoveCommand(string(c.tier), "my-fix", c.scope)
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "steer" {
			t.Fatalf("%s: %q does not start with steer", c.tier, line)
		}
		found, rest, err := rootCmd.Find(fields[1:])
		if err != nil || found.CommandPath() != c.path || !found.Runnable() {
			t.Fatalf("%s: %q resolves to %q (err %v), want %s", c.tier, line, found.CommandPath(), err, c.path)
		}
		scope = claude.ProjectScope
		if err := found.ParseFlags(rest); err != nil {
			t.Fatalf("%s: %q: %v", c.tier, line, err)
		}
		if args := found.Flags().Args(); !slices.Equal(args, []string{"my-fix"}) {
			t.Errorf("%s: %q passes arguments %q, want [my-fix]", c.tier, line, args)
		}
		if c.scope != "" && currentScope() != c.scope {
			t.Errorf("%s: %q selects scope %q, want %q", c.tier, line, currentScope(), c.scope)
		}
	}
}

// TestRemoveHintNamesEveryOtherTiersCommand pins that a remove miss in any tier
// points at the three other tiers' remove commands, each runnable with the
// identifier the operator passed. An identifier of another tier reaches a remove
// command as a missing fix, and the bare not-found answer names no other place to
// look.
func TestRemoveHintNamesEveryOtherTiersCommand(t *testing.T) {
	t.Cleanup(func() {
		scope = claude.ProjectScope
		rootCmd.PersistentFlags().Lookup("scope").Changed = false
	})
	ladder := []tier.Kind{tier.Guard, tier.Instruction, tier.Review, tier.Check}
	for _, own := range ladder {
		for _, s := range []string{claude.ProjectScope, claude.GlobalScope} {
			hint := otherTierRemoveHint(own, "my-fix", s)
			lead := status.RemoveCommandName(string(own)) + " takes a " + status.ArtifactNoun(string(own))
			if !strings.HasPrefix(hint, lead) {
				t.Errorf("%s/%s: the hint does not open with %q:\n%s", own, s, lead, hint)
			}
			if strings.Contains(hint, status.RemoveCommand(string(own), "my-fix", "")) {
				t.Errorf("%s/%s: the hint repeats the command it follows:\n%s", own, s, hint)
			}
			for _, k := range ladder {
				if k == own {
					continue
				}
				hintScope := ""
				if s == claude.GlobalScope && k != tier.Check {
					hintScope = s
				}
				want := status.RemoveCommand(string(k), "my-fix", hintScope)
				if !strings.Contains(hint, want) {
					t.Errorf("%s/%s: the hint does not offer %q:\n%s", own, s, want, hint)
				}
				scope = claude.ProjectScope
				fields := strings.Fields(want)
				found, rest, err := rootCmd.Find(fields[1:])
				if err != nil || !found.Runnable() {
					t.Fatalf("%s/%s: %q resolves to %q (err %v)", own, s, want, found.CommandPath(), err)
				}
				if err := found.ParseFlags(rest); err != nil {
					t.Fatalf("%s/%s: %q: %v", own, s, want, err)
				}
				if args := found.Flags().Args(); !slices.Equal(args, []string{"my-fix"}) {
					t.Errorf("%s/%s: %q passes arguments %q, want [my-fix]", own, s, want, args)
				}
			}
		}
	}
}

// TestEveryTiersRemoveMissCarriesTheHint pins that the miss each tier's own remove
// command answers with carries the other tiers' commands. Each command resolves its
// own tier only, so an operator holding a bare id has nothing else to go on.
func TestEveryTiersRemoveMissCarriesTheHint(t *testing.T) {
	root := archiveRepo(t)
	t.Cleanup(func() { scope = claude.ProjectScope })
	if _, _, err := checks.Init(root); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command *cobra.Command
		own     tier.Kind
		miss    string
	}{
		{rulesRemoveCmd, tier.Instruction, "rule not found"},
		{commitGateAddendumRemoveCmd, tier.Review, "addendum entry not found"},
		{checkRemoveCmd, tier.Check, "no check is declared"},
	}
	for _, c := range cases {
		scope = claude.ProjectScope
		var out bytes.Buffer
		c.command.SetOut(&out)
		err := c.command.RunE(c.command, []string{"my-fix"})
		if err == nil || !strings.Contains(err.Error(), c.miss) {
			t.Fatalf("%s in %s: err = %v, want %q", c.command.CommandPath(), root, err, c.miss)
		}
		if got := err.Error(); !strings.Contains(got, otherTierRemoveHint(c.own, "my-fix", claude.ProjectScope)) {
			t.Errorf("%s: the miss carries no cross-tier hint:\n%s", c.command.CommandPath(), got)
		}
	}
}

// TestGroupCommandRejectsUnknownSubcommand pins that a group command fails on a
// subcommand it does not have, and still shows its help with no argument.
func TestGroupCommandRejectsUnknownSubcommand(t *testing.T) {
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	rootCmd.SetArgs([]string{"hooks", "remove", "my-guard"})
	err := Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown command "remove" for "steer hooks"`) {
		t.Errorf("steer hooks remove my-guard: err = %v, want an unknown command error", err)
	}

	rootCmd.SetArgs([]string{"hooks"})
	if err := Execute(); err != nil {
		t.Errorf("steer hooks: err = %v, want its help", err)
	}
}

// seedMovedGuard writes a global store whose entry a owns z.py beside an unowned
// a.py, each guard blocking with its own file's name.
func seedMovedGuard(t *testing.T) string {
	t.Helper()
	tempSettingsHome(t)
	oldScope := scope
	scope = claude.GlobalScope
	t.Cleanup(func() { scope = oldScope })
	if err := claude.EnsureDirectories(claude.GlobalScope); err != nil {
		t.Fatal(err)
	}
	dir, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "z"} {
		source := fmt.Sprintf("import json\n\ndef main():\n    print(json.dumps({\"decision\": \"block\", \"stopReason\": \"from %s.py\"}))\n\nmain()\n", name)
		if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &claude.Manifest{Hooks: map[string]claude.ManifestEntry{"a": {File: "z.py", Rule: "rule of a"}}}
	if err := claude.SaveManifest(claude.GlobalScope, manifest); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The confirmation names the file the removal acts on, not the file with the id's stem.
func TestRemoveConfirmationNamesTheEntrysFile(t *testing.T) {
	dir := seedMovedGuard(t)
	stdin, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := feed.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	feed.Close()
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = oldStdin; stdin.Close() })

	out := captureStdout(t, func() {
		if err := removeCmd.RunE(removeCmd, []string{"a"}); err != nil {
			t.Fatalf("remove: %v", err)
		}
	})
	flat := strings.Join(strings.Fields(out), "")
	if !strings.Contains(flat, "z.py") || strings.Contains(flat, "/a.py") {
		t.Errorf("the confirmation does not name z.py alone:\n%s", out)
	}
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("the declined removal did not cancel:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "z.py")); err != nil {
		t.Errorf("the declined removal touched z.py: %v", err)
	}
}

// An ad-hoc steer test runs the file the guard's entry names.
func TestTestWithCodeRunsTheEntrysFile(t *testing.T) {
	seedMovedGuard(t)
	if err := testCmd.Flags().Set("code", "x"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testCmd.Flags().Set("code", "") })

	out := captureStdout(t, func() {
		if err := testCmd.RunE(testCmd, []string{"a"}); err != nil {
			t.Fatalf("test: %v", err)
		}
	})
	if !strings.Contains(out, "from z.py") || strings.Contains(out, "from a.py") {
		t.Errorf("steer test a did not run z.py alone:\n%s", out)
	}
}

// The refusal of an unowned file offers no other tier's remove command.
func TestRemoveOfAnUnownedFileCarriesNoTierHint(t *testing.T) {
	seedMovedGuard(t)
	err := removeCmd.RunE(removeCmd, []string{"a.py"})
	if !errors.Is(err, claude.ErrUnownedGuardFile) {
		t.Fatalf("remove a.py = %v, want ErrUnownedGuardFile", err)
	}
	if strings.Contains(err.Error(), "other tier") {
		t.Errorf("the refusal carries the tier hint: %v", err)
	}
}

// The removal reports the unowned file it leaves running, in the confirmation and
// after a forced removal alike; the next removal by that name takes the file.
func TestRemoveReportsTheStemFileItLeaves(t *testing.T) {
	dir := seedMovedGuard(t)
	if err := removeCmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeCmd.Flags().Set("force", "false") })
	out := captureStdout(t, func() {
		if err := removeCmd.RunE(removeCmd, []string{"a"}); err != nil {
			t.Fatalf("remove: %v", err)
		}
	})
	if !strings.Contains(strings.Join(strings.Fields(out), " "), "Stays: a.py") {
		t.Errorf("the output does not report a.py:\n%s", out)
	}
	captureStdout(t, func() {
		if err := removeCmd.RunE(removeCmd, []string{"a.py"}); err != nil {
			t.Fatalf("second remove: %v", err)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "a.py")); !os.IsNotExist(err) {
		t.Errorf("a.py was not removed: %v", err)
	}
}
