package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/gate"
)

var hostFormer string

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Show the name Steer records for this machine, and declare a former one",
	Long: `Steer stamps review history, fixes and session history with a machine name. It
pins that name from the hostname on first use, in ~/.steer/host, so a later hostname
change opens no new history. A fix registered under an earlier name reads as another
machine's until --former declares that name as this machine's.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if hostFormer != "" {
			slug, err := gate.AddFormerHost(hostFormer)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Declared %s as a former name of this machine.\n", slug)
		}
		fmt.Fprintln(cmd.OutOrStdout(), gate.HostID())
		for _, former := range gate.FormerHosts() {
			fmt.Fprintf(cmd.OutOrStdout(), "former: %s\n", former)
		}
		return nil
	},
}

func init() {
	hostCmd.Flags().StringVar(&hostFormer, "former", "", "a name this machine used before")
	rootCmd.AddCommand(hostCmd)
}
