package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var removeCmd = &cobra.Command{
	Use:   "remove <hook-name-or-path>",
	Short: "Remove an individual hook",
	Long: `Remove deletes an individual hook file and re-syncs all steer-managed
artifacts:
- removes hook file from hooks directory
- removes hook entry from steer-manifest.json
- removes associated rule line from CLAUDE.md
- re-consolidates steer-hooks.py
- removes master hook registration when no hooks remain`,
	Example: `  steer remove no-var-hook --force
  steer remove .claude/hooks/no-var-hook.py`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🗑️  Remove Hook"))
		fmt.Println()

		scope := currentScope()
		identifier := args[0]
		force, _ := cmd.Flags().GetBool("force")

		hook, err := claude.ResolveHook(scope, identifier)
		if err != nil {
			return err
		}

		if !force {
			fmt.Println(styles.WarningBox.Render(fmt.Sprintf(
				"Hook: %s\nPath: %s\nScope: %s",
				hook.Name,
				hook.Path,
				scope,
			)))
			fmt.Printf("%s ", styles.BrandText.Render("Are you sure? (y/N):"))
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println(styles.InfoBox.Render("Cancelled."))
				return nil
			}
		}

		utils.Working("Removing hook and syncing managed artifacts...")
		if _, err := claude.RemoveManagedHook(scope, identifier); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf("Removed hook: %s", hook.Name)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().Bool("force", false, "Skip confirmation prompt")
}
