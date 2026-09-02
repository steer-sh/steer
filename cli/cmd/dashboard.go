package cmd

import (
	"github.com/spf13/cobra"

	"github.com/steer-sh/steer-cli/internal/tui"
)

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"top"},
	Short:   "Interactive control panel over the steer loop",
	Long: `dashboard opens an interactive control panel over the whole loop. It is the
mutation surface: six panels — channels, candidates, classes, hooks & presets,
reviewer and health — each acting through the same package functions the equivalent
CLI commands call. The read model is the same snapshot 'steer status' renders,
recomposed after every write.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// The dashboard is the mutation surface, so it migrates before it reads:
		// every panel that writes goes on to write the path this snapshot names.
		if err := prepareState(); err != nil {
			return err
		}
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		cfg, _, _ := resolvedGateConfig()
		return tui.Run(cfg, currentScope(), targets...)
	},
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
}
