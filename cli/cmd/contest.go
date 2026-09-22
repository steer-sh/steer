package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/firingaudit"
	"github.com/steer-sh/steer-cli/internal/statestore"
)

var contestCmd = &cobra.Command{
	Use:   "contest FIRING_ID REASON",
	Short: "Record that a guard's block was wrong for this task",
	Long: `Contest records a blocked agent's statement that one block was wrong for its
task. A block's message names its firing identifier and this command.

A contest is feedback: the block stays in effect, and nothing about the guard
changes. REASON names the fact about this task that the rule did not account
for. The report shows contests per guard; removing a guard stays the user's
decision.`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		firingID := strings.TrimSpace(args[0])
		reason := strings.TrimSpace(strings.Join(args[1:], " "))
		if !adjudication.ValidFiringID(firingID) {
			return fmt.Errorf("invalid firing identifier %q: copy the 32 hexadecimal characters after \"firing:\" in the block message", firingID)
		}
		if err := prepareState(); err != nil {
			return err
		}
		dir, err := statestore.Prepare(adjudication.Component)
		if err != nil {
			return err
		}
		rows, _ := firingaudit.Load(evidence.GuardStores()...)
		cwd, _ := os.Getwd()
		c, err := firingaudit.Contest(rows, dir, firingID, reason, cwd)
		if err != nil {
			return err
		}
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), c)
		}
		out := cmd.OutOrStdout()
		if c.Matched {
			fmt.Fprintf(out, "Contest recorded for %s (firing %s). The block remains in effect.\n",
				c.HookID, c.FiringID)
			return nil
		}
		fmt.Fprintf(out, "Contest recorded; no matching firing row was found in the stores searched. "+
			"The block remains in effect.\n")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(contestCmd)
	contestCmd.Flags().BoolVar(&auditJSON, "json", false, "emit JSON")
}
