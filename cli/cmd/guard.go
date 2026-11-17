package cmd

import "github.com/spf13/cobra"

var guardCmd = &cobra.Command{
	Use:   "guard",
	Short: "Register, list, test and remove guards — PreToolUse Python hooks",
}

// topLevelAlias keeps a guard command answering under its former name, steer
// <name>, hidden from help. It shares c's flags and run function.
func topLevelAlias(c *cobra.Command) *cobra.Command {
	alias := &cobra.Command{
		Use:     c.Use,
		Short:   c.Short,
		Long:    c.Long,
		Example: c.Example,
		Args:    c.Args,
		RunE:    c.RunE,
		Hidden:  true,
	}
	alias.Flags().AddFlagSet(c.Flags())
	return alias
}

func init() {
	rootCmd.AddCommand(guardCmd)
}
