package cmd

import (
	"github.com/spf13/cobra"
)

var commitGateMineCmd = &cobra.Command{
	Use:   "mine",
	Short: "Mine the adjudication ledger and score the fixes it motivates",
	Long: `mine reads the commit gate's adjudication ledger, groups its rows into chains
(one per commit attempt), and scores the interventions that a class's chain-level
incidence motivates. It is the reader half of the loop the gate's review half writes.

Its intervention subcommands are the review-findings alias of ` + "`steer fix`" + `,
built from one factory: the two trees carry the same flags and call the same functions.`,
}

func init() {
	commitGateCmd.AddCommand(commitGateMineCmd)
	v, cmds := newInterventionCommands(true)
	commitGateMineCmd.AddCommand(cmds...)
	commitGateMineCmd.PersistentFlags().StringVar(&v.ledger, "ledger", "",
		"Override the adjudication ledger path (a single file, not the shard glob)")
}
