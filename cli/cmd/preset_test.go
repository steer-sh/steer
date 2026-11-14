package cmd

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/protection"
)

// The self-protection guard skips a flag's value by name, so its table must hold
// every value-taking flag of the commands it reads, and only those.
func TestSelfProtectionValueFlagTableMatchesTheCLI(t *testing.T) {
	want := map[string]bool{}
	collect := func(flag *pflag.Flag) {
		if flag.NoOptDefVal != "" {
			return
		}
		want["--"+flag.Name] = true
		if flag.Shorthand != "" {
			want["-"+flag.Shorthand] = true
		}
	}
	rootCmd.PersistentFlags().VisitAll(collect)
	commands := []*cobra.Command{removeCmd, registerCmd, presetCmd, configCmd}
	commands = append(commands, presetCmd.Commands()...)
	commands = append(commands, configCmd.Commands()...)
	for _, command := range commands {
		command.PersistentFlags().VisitAll(collect)
		command.Flags().VisitAll(collect)
	}

	table := regexp.MustCompile(`(?s)STEER_VALUE_FLAGS = \{(.*?)\n\}`).FindSubmatch(protection.Desired().Content)
	if table == nil {
		t.Fatal("STEER_VALUE_FLAGS missing from the guard source")
	}
	var got []string
	for _, match := range regexp.MustCompile(`"(-[^"]+)"`).FindAllSubmatch(table[1], -1) {
		got = append(got, string(match[1]))
	}
	sort.Strings(got)

	var expected []string
	for flag := range want {
		expected = append(expected, flag)
	}
	sort.Strings(expected)
	if !slices.Equal(got, expected) {
		t.Fatalf("guard table = %v, CLI value-taking flags = %v", got, expected)
	}
}

// list names each guard an earlier Steer installed from a preset, with the commands
// that remove them in the listed scope, and says when there is none.
func TestPresetListNamesTheOrphansAndTheirRemoval(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	out := captureStdout(t, func() {
		if err := presetListCmd.RunE(presetListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "ships no presets") {
		t.Fatalf("an empty list does not say no preset ships:\n%s", out)
	}

	scope = claude.GlobalScope
	addOrphanFactoryHook(t, claude.GlobalScope, "sql-injection-prevention", "sql-injection-prevention", "sql safety")
	out = captureStdout(t, func() {
		if err := presetListCmd.RunE(presetListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "sql-injection-prevention") || !strings.Contains(out, "--prune-orphans --scope global") {
		t.Fatalf("a global list does not name the orphan and its global removal:\n%s", out)
	}
}

// A guard's block names `steer preset disable <preset>` as its removal, so that
// command removes a guard whose preset this Steer no longer ships.
func TestPresetDisableRemovesAGuardWhosePresetLeftTheCatalog(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	addOrphanFactoryHook(t, claude.ProjectScope, "sql-injection-prevention", "sql-injection-prevention", "sql safety")

	if err := presetDisableCmd.RunE(presetDisableCmd, []string{"sql-injection-prevention"}); err != nil {
		t.Fatalf("disable of a removed preset: %v", err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Hooks) != 0 {
		t.Fatalf("guards left after disable: %v", manifest.Hooks)
	}
	if err := presetDisableCmd.RunE(presetDisableCmd, []string{"sql-injection-prevention"}); err == nil {
		t.Fatal("a second disable reported success")
	}
}
