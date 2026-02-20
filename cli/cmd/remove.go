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

		rule, err := claude.GetRule(scope, hook.Name)
		if err != nil {
			return err
		}

		utils.Working("Deleting hook file...")
		if err := os.Remove(hook.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete hook file: %w", err)
		}

		utils.Registering("Updating manifest...")
		if err := claude.RemoveHook(scope, hook.Name); err != nil {
			return err
		}

		if rule != "" {
			utils.Saving("Updating CLAUDE.md...")
			if err := claude.RemoveRule(rule, scope); err != nil {
				return err
			}
		}

		utils.Working("Rebuilding master hook...")
		if err := claude.ConsolidateHooks(scope); err != nil {
			return err
		}

		remaining, err := claude.ListIndividualHooks(scope)
		if err != nil {
			return err
		}

		if len(remaining) == 0 {
			utils.Working("Removing steer master hook from settings.json...")
			if err := claude.RemoveMasterHook(scope); err != nil {
				return err
			}
		} else {
			utils.Working("Refreshing steer master hook registration...")
			if err := claude.RegisterMasterHook(scope); err != nil {
				return err
			}
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
