package cmd

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagSpec is one flag's public surface.
type flagSpec struct{ name, usage, def string }

func specsOf(c *cobra.Command) []flagSpec {
	var out []flagSpec
	c.Flags().VisitAll(func(f *pflag.Flag) {
		out = append(out, flagSpec{f.Name, f.Usage, f.DefValue})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
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

func TestTheFixTreeKeepsItsFormerName(t *testing.T) {
	if interventionCmd.Name() != "fix" {
		t.Errorf("the primary tree is named %q, want \"fix\"", interventionCmd.Name())
	}
	if !interventionCmd.HasAlias("intervention") {
		t.Error("steer intervention no longer resolves to the fix tree")
	}
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
