package cmd

import (
	"slices"
	"testing"
)

// Each guard command answers under steer guard and, hidden from help, under its
// former top-level name with the same flags, so a skill or script that types the
// former name keeps working.
func TestGuardCommandsAnswerUnderTheGroupAndTheirFormerName(t *testing.T) {
	for _, name := range []string{"register", "list", "remove", "test"} {
		sub, _, err := rootCmd.Find([]string{"guard", name})
		if err != nil || sub.Name() != name || sub.Parent() != guardCmd {
			t.Fatalf("steer guard %s resolves to %v (%v)", name, sub.CommandPath(), err)
		}
		alias, _, err := rootCmd.Find([]string{name})
		if err != nil || alias.Name() != name || alias.Parent() != rootCmd {
			t.Fatalf("steer %s resolves to %v (%v)", name, alias.CommandPath(), err)
		}
		if !alias.Hidden {
			t.Errorf("steer %s is listed in help beside steer guard %s", name, name)
		}
		if !slices.Equal(specsOf(alias), specsOf(sub)) {
			t.Errorf("steer %s flags = %v, steer guard %s flags = %v", name, specsOf(alias), name, specsOf(sub))
		}
		if alias.RunE == nil {
			t.Errorf("steer %s runs nothing", name)
		}
	}
}
