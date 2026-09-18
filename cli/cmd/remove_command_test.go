package cmd

import (
	"bytes"
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
