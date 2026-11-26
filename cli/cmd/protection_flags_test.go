package cmd

import (
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steer-sh/steer-cli/internal/protection"
)

// The self-protection guard skips a flag's value by name, so its table must hold
// every value-taking flag of the commands it reads, and only those.
//
// pins existing behaviour
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
	commands := []*cobra.Command{guardCmd, removeCmd, registerCmd, configCmd, sleepCmd, wakeCmd}
	commands = append(commands, configCmd.Commands()...)
	commands = append(commands, sleepCmd.Commands()...)
	commands = append(commands, wakeCmd.Commands()...)
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
