package cmd

import (
	"slices"
	"strings"
	"testing"

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
