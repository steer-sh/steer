package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write ~/.steer/config.json",
	Long: `config reads and writes Steer's harness-neutral operator settings.

The file is global: one reviewer configuration for every repository and every
harness. Values resolve in this order — the shipped defaults, this file, the
environment, then a command's own flags — so an environment variable or a flag
outranks anything set here.`,
	Example: `  steer config show
  steer config get reviewer.effort
  steer config set reviewer.provider codex
  steer config set reviewer.model ""`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show every setting, the value in force, and where it comes from",
	RunE: func(cmd *cobra.Command, args []string) error {
		file, err := config.Load()
		if err != nil {
			return err
		}
		cfg, _, resolveErr := resolvedGateConfig()

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Steer Settings"))
		fmt.Println()
		fmt.Printf("  File  %s%s\n", steerpath.ConfigFile(), configFileNote())
		fmt.Println()
		for _, f := range settings.Fields {
			fmt.Printf("  %-22s %-24s %s\n", f.Name, f.Effective(cfg), styles.MutedText.Render(settings.Source(f, file)))
		}
		fmt.Println()
		if resolveErr != nil {
			utils.Warning(resolveErr.Error())
		}
		return nil
	},
}

func configFileNote() string {
	if _, err := os.Stat(steerpath.ConfigFile()); os.IsNotExist(err) {
		return styles.MutedText.Render("  (not written yet — every value is the shipped default)")
	}
	return ""
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one stored value",
	Long: `get prints the value the settings file stores, and nothing when the key is
unset. It is the file's own value, not the value in force: run "steer config show"
to see what an environment variable overrides.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := settings.Find(args[0])
		if err != nil {
			return err
		}
		file, err := config.Load()
		if err != nil {
			return err
		}
		fmt.Println(f.Get(file))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Write one value",
	Long: `set writes one key to ~/.steer/config.json. An empty value clears the key,
which returns it to the shipped default.

A change to the reviewer's identity — its provider, model, effort or run count —
changes the instrument the ledger stamps on every later review row.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := settings.Find(args[0])
		if err != nil {
			return err
		}
		value := strings.TrimSpace(args[1])
		file, err := config.Load()
		if err != nil {
			return err
		}
		next := file
		if err := f.Set(&next, value); err != nil {
			return err
		}
		if next == file {
			utils.Info(fmt.Sprintf("%s is already %s.", f.Name, settings.StoredLabel(value)))
			return nil
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Steer Settings"))
		fmt.Println()
		// An effort no provider documents is warned about by the reviewer resolver,
		// which previewReviewer runs; warning here too would print it twice.
		if f.Identity {
			previewReviewer(next)
		}
		if err := config.Save(next); err != nil {
			return err
		}
		utils.Success(fmt.Sprintf("%s = %s", f.Name, settings.StoredLabel(value)))
		if f.Identity {
			// A registration naming the reviewer outranks this file, so the value just
			// written is not the one that runs until that registration is stripped.
			reportPinnedRegistrations()
		}
		if f.Name == "reviewer.timeout" {
			// The engine reads the new value on the next firing; the harness's own
			// hook timeout is a registered number, and a review killed by the smaller
			// wall clock lands the commit ungated with no row at all.
			utils.Info("The harness hook's wall clock is this timeout plus a margin, and it is " +
				"written at registration time. Re-run `steer commit-gate install` for the new " +
				"timeout to take full effect.")
		}
		if source := settings.Source(f, next); strings.HasPrefix(source, "env ") {
			utils.Warning(fmt.Sprintf("%s is set and outranks the settings file, so this "+
				"value is not in force here.", strings.TrimPrefix(source, "env ")))
		}
		return nil
	},
}

// previewReviewer resolves the reviewer the pending change would run, so its warnings
// print before the change is written.
func previewReviewer(next config.Config) {
	if _, err := settings.Preview(next, warnOnce); err != nil {
		utils.Warning(err.Error())
	}
}

// configProvidersCmd lists the reviewers and whether each can run here. It is
// what an install flow offers a chooser from, so it reads the one provider set
// rather than carrying its own copy of the names.
var configProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List the reviewers and whether each one can run here",
	Long: `providers lists every reviewer Steer can run, with the executable it calls and
whether that executable resolves on this process's PATH. The openai-compatible
reviewer calls no executable: its row reads the configured base URL and model, and
whether this process finds the API key. It sends no request.

The reviewer runs from the harness's environment, whose PATH and variables may
differ. So a "missing" reading proves the executable or key is absent from this
process; an "available" one can never prove the reviewer runs.

The reviewer is independent of the agent writing the code: any of these may
review commits made from any harness.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Reviewers"))
		fmt.Println()
		cfg := gateConfig()
		for _, p := range reviewers.Providers() {
			probe := reviewers.ProbeConfig(p.ID, settings.ReviewerOptions(cfg))
			state := probe.State
			if p.API && probe.Unset != "" {
				state += " (" + probe.Unset + ")"
			}
			fmt.Printf("  %-18s %-22s %-10s %s\n", p.ID, p.Label, probe.Executable,
				styles.MutedText.Render(state))
		}
		fmt.Println()
		return nil
	},
}

func init() {
	// The key documentation is generated from the one table, so a key added there
	// can never be missing from the command's help.
	configCmd.Long += "\n\nSettings:\n" + settings.KeyHelp()
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configProvidersCmd)
}
