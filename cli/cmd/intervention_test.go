package cmd

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagSpec is one flag's public surface: the two trees must agree on all of it.
type flagSpec struct{ name, usage, def string }

func specsOf(c *cobra.Command) []flagSpec {
	var out []flagSpec
	c.Flags().VisitAll(func(f *pflag.Flag) {
		out = append(out, flagSpec{f.Name, f.Usage, f.DefValue})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func subcommandNames(c *cobra.Command) []string {
	var out []string
	for _, sub := range c.Commands() {
		out = append(out, strings.Fields(sub.Use)[0])
	}
	sort.Strings(out)
	return out
}

func find(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range parent.Commands() {
		if strings.Fields(sub.Use)[0] == name {
			return sub
		}
	}
	t.Fatalf("%s has no subcommand %q", parent.Name(), name)
	return nil
}

// TestBothInterventionTreesCarryTheSameSurface pins the compatibility alias: whatever
// `steer intervention` accepts, `steer commit-gate mine` accepts identically. The one
// permitted difference is `findings`, which reads the review corpus rather than
// managing an intervention.
func TestBothInterventionTreesCarryTheSameSurface(t *testing.T) {
	want := []string{"describe", "list", "match", "register", "reject", "retire", "scope", "score"}
	if got := subcommandNames(interventionCmd); !equalStrings(got, want) {
		t.Errorf("steer intervention subcommands = %v, want %v", got, want)
	}
	if got := subcommandNames(commitGateMineCmd); !equalStrings(got, append([]string{"findings"}, want...)) {
		t.Errorf("commit-gate mine subcommands = %v, want findings plus %v", got, want)
	}
	for _, name := range want {
		a, b := specsOf(find(t, interventionCmd, name)), specsOf(find(t, commitGateMineCmd, name))
		if len(a) != len(b) {
			t.Fatalf("%s: %d flags on intervention, %d on mine", name, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("%s flag %d: intervention %+v, mine %+v", name, i, a[i], b[i])
			}
		}
	}
}

// TestTheTreesDoNotShareFlagStorage guards the factory: two cobra commands bound to one
// variable would carry a value set on the other tree into this one.
func TestTheTreesDoNotShareFlagStorage(t *testing.T) {
	a := find(t, interventionCmd, "score")
	b := find(t, commitGateMineCmd, "score")
	if err := a.Flags().Set("id", "only-here"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Flags().Set("id", "") }()
	if got := b.Flags().Lookup("id").Value.String(); got != "" {
		t.Errorf("mine score --id = %q; the trees share storage", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
