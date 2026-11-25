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
	Use:   "contest {FIRING_ID | --run RUN_ID} REASON",
	Short: "Record that a guard's or check.sh's block was wrong for this task",
	Long: `Contest records a blocked agent's statement that one block was wrong for its
task. A guard block's message names its firing identifier and this command; a
check.sh Stop block's message names its run and this command with --run.

A contest is feedback: the block stays in effect, and nothing about the guard or
check changes. REASON names the fact about this task that the rule or check did
not account for. The report shows contests per guard and per repository;
removing a guard or changing a check stays the user's decision.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if contestRun != "" {
			return cobra.MinimumNArgs(1)(cmd, args)
		}
		return cobra.MinimumNArgs(2)(cmd, args)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if contestRun != "" {
			return contestCheck(cmd, strings.TrimSpace(contestRun), strings.TrimSpace(strings.Join(args, " ")))
		}
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

// contestCheck records a contest of the check block of run runID.
func contestCheck(cmd *cobra.Command, runID, reason string) error {
	if !adjudication.ValidRunID(runID) {
		return fmt.Errorf("invalid run identifier %q: copy the identifier after \"steer check run:\" in the block message", runID)
	}
	if err := prepareState(); err != nil {
		return err
	}
	dir, err := statestore.Prepare(adjudication.Component)
	if err != nil {
		return err
	}
	cases, _ := firingaudit.LoadCheckCases()
	cwd, _ := os.Getwd()
	c, err := firingaudit.ContestCheck(cases, dir, runID, reason, cwd)
	if err != nil {
		return err
	}
	if auditJSON {
		return writeJSON(cmd.OutOrStdout(), c)
	}
	out := cmd.OutOrStdout()
	if c.Matched {
		fmt.Fprintf(out, "Contest recorded for check.sh in %s (run %s). The block remains in effect.\n", c.Repo, c.RunID)
		return nil
	}
	fmt.Fprintf(out, "Contest recorded; no recorded Stop block on this machine has run %s. The block remains in effect.\n", runID)
	return nil
}

var contestRun string

func init() {
	rootCmd.AddCommand(contestCmd)
	contestCmd.Flags().BoolVar(&auditJSON, "json", false, "emit JSON")
	contestCmd.Flags().StringVar(&contestRun, "run", "", "the check run a check.sh Stop block named")
}
