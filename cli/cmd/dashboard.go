package cmd

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/steer-sh/steer-cli/internal/tui"
)

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"top"},
	Short:   "Interactive control panel over the steer loop",
	Long: `dashboard opens an interactive control panel over the whole loop. It is the
mutation surface: five panels — Arming, Problems, Fixes, Reviewer and Diagnostics —
each acting through the same package functions the equivalent CLI commands call. It
reads the snapshot 'steer status' composes, recomposed after every write, and needs
an interactive terminal; a script or an agent reads 'steer status --json'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTerminal(os.Stdin.Fd(), os.Stdout.Fd()); err != nil {
			return err
		}
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

// errNoTerminal is the refusal a script or an agent gets: the dashboard draws on
// an interactive terminal and prints nothing anywhere else.
var errNoTerminal = errors.New("steer dashboard needs an interactive terminal; " +
	"a script or an agent reads steer status --json")

// requireTerminal refuses unless both stdin and stdout are terminals.
func requireTerminal(in, out uintptr) error {
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return errNoTerminal
	}
	return nil
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
}
