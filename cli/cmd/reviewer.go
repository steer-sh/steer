package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/apikey"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var reviewerCmd = &cobra.Command{
	Use:   "reviewer",
	Short: "Manage the API key of the openai-compatible reviewer",
}

var reviewerKeyCmd = &cobra.Command{
	Use:   "key",
	Short: "Store, check or remove the reviewer API key",
	Long: `key manages the API key the openai-compatible reviewer sends. The key never
goes in ~/.steer/config.json, which every user can read.

At review time the gate reads the variable reviewer.api.key_env names, when it is
set and not empty in the gate's own process, and otherwise the key file. The gate
runs in the harness's hook process, whose environment can differ from your shell's;
the key file has no such gap.`,
	Example: `  steer reviewer key set
  steer reviewer key status
  steer reviewer key remove`,
}

var reviewerKeySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Read the key from the terminal, with no echo, into the key file",
	Long: `set reads the key from this terminal with no echo and writes it to a file only
you can read. It refuses input that is not a terminal, so the key cannot pass
through a pipe or land in shell history. Run it in your own terminal.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return errors.New("stdin is not a terminal; run steer reviewer key set in your own terminal, " +
				"so the key passes through no pipe or shell history")
		}
		fmt.Fprint(os.Stderr, "API key (input hidden): ")
		key, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("read the key: %w", err)
		}
		path, err := apikey.Write(string(key))
		if err != nil {
			return err
		}
		utils.Success("Stored the reviewer API key in " + path)
		if _, st := apikey.Find(gateConfig().ReviewerAPIKeyEnv); st.Source == apikey.SourceEnv {
			utils.Warning(fmt.Sprintf("$%s is set here and outranks the key file.", st.Env))
		}
		return nil
	},
}

var reviewerKeyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print where this process finds the key: env <NAME>, file, or missing",
	Long: `status prints where this process finds the key, and never the key or any part
of it. The gate runs in the harness's process, whose environment can differ, so an
"env" reading here does not prove the gate sees the same variable.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st := apikey.Find(gateConfig().ReviewerAPIKeyEnv)
		fmt.Println(st.Label())
		if st.Source == apikey.SourceMissing {
			fmt.Fprintln(os.Stderr, st.Missing())
		}
		return nil
	},
}

var reviewerKeyRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Delete the key file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, removed, err := apikey.Remove()
		if err != nil {
			return err
		}
		if !removed {
			utils.Info("No key file at " + path + ".")
			return nil
		}
		utils.Success("Removed " + path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reviewerCmd)
	reviewerCmd.AddCommand(reviewerKeyCmd)
	reviewerKeyCmd.AddCommand(reviewerKeySetCmd, reviewerKeyStatusCmd, reviewerKeyRemoveCmd)
}
