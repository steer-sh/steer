package cmd

import (
	"sort"
	"strings"
	"testing"
	"time"

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
// `steer fix` accepts, `steer commit-gate mine` accepts identically. The one
// permitted difference is `findings`, which reads the review corpus rather than
// managing an intervention.
func TestTheFixTreeKeepsItsFormerName(t *testing.T) {
	if interventionCmd.Name() != "fix" {
		t.Errorf("the primary tree is named %q, want \"fix\"", interventionCmd.Name())
	}
	if !interventionCmd.HasAlias("intervention") {
		t.Error("steer intervention no longer resolves to the fix tree")
	}
}

func TestBothInterventionTreesCarryTheSameSurface(t *testing.T) {
	want := []string{"describe", "list", "match", "register", "reject", "retire", "scope", "score"}
	if got := subcommandNames(interventionCmd); !equalStrings(got, want) {
		t.Errorf("steer fix subcommands = %v, want %v", got, want)
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

// `steer fix score` reads a stored snapshot and rebuilds nothing, so its note says
// how old that snapshot is and names the one command that refreshes it. Without the
// age, an unread window reads as a quiet one.
func TestSnapshotFreshnessNoteDatesTheSnapshotAndNamesTheRefresh(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	note := snapshotFreshnessNote(now.Add(-72*time.Hour), now)
	if !strings.Contains(note, "3 days ago") {
		t.Errorf("the note does not state the snapshot's age:\n%s", note)
	}
	if !strings.Contains(note, "2026-09-15T12:00:00Z") {
		t.Errorf("the note does not date the snapshot:\n%s", note)
	}
	if !strings.Contains(note, "steer report refresh") {
		t.Errorf("the note names no refresh command:\n%s", note)
	}

	unknown := snapshotFreshnessNote(time.Time{}, now)
	if strings.Contains(unknown, "days ago") {
		t.Errorf("an unrecorded refresh must not print an age:\n%s", unknown)
	}
	if !strings.Contains(unknown, "steer report refresh") {
		t.Errorf("the note names no refresh command:\n%s", unknown)
	}
}
