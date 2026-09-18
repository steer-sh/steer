package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/tier"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var removeCmd = &cobra.Command{
	Use:   "remove <hook-name-or-path>",
	Short: "Remove an individual hook",
	Long: `Remove deletes an individual hook file and re-syncs all steer-managed
artifacts:
- archives the hook file, its fixtures and its manifest entry under archive/
- removes hook file from hooks directory
- removes hook entry from steer-manifest.json
- removes associated rule line from CLAUDE.md
- re-consolidates steer-hooks.py
- removes master hook registration when no hooks remain`,
	Example: `  steer remove no-var-hook --force
  steer remove no-var-hook --reason "blocks legitimate var in shell scripts"
  steer remove .steer/hooks/no-var-hook.py`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🗑️  Remove Hook"))
		fmt.Println()

		scope := currentScope()
		identifier := args[0]
		force, _ := cmd.Flags().GetBool("force")
		reason, _ := cmd.Flags().GetString("reason")

		hook, err := claude.ResolveHook(scope, identifier)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, otherTierRemoveHint(identifier, scope))
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

		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		utils.Working("Removing hook and syncing managed artifacts...")
		result, err := claude.RemoveManagedHook(scope, identifier, claude.RemoveOptions{Reason: strings.TrimSpace(reason)}, targets...)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Removed hook: %s\nHarness: %s\nArchived: %s", hook.Name, harnessNames(targets), result.ArchivePath)))
		printFixtureVerification(result.Verification)
		return nil
	},
}

// otherTierRemoveHint names the remove command of each tier but the guard's, with
// this identifier. A guard is all `steer remove` resolves, so an ID of another tier
// arrives here as a missing guard. Checks are archived and removed per repository,
// so the check line carries no scope.
func otherTierRemoveHint(identifier, scope string) string {
	hintScope := ""
	if scope == claude.GlobalScope {
		hintScope = scope
	}
	lines := []string{"steer remove takes a guard. Each other tier has its own remove command:"}
	for _, t := range []tier.Kind{tier.Instruction, tier.Review} {
		lines = append(lines, "  "+status.RemoveCommand(string(t), identifier, hintScope))
	}
	lines = append(lines, "  "+status.RemoveCommand(string(tier.Check), identifier, ""))
	return strings.Join(lines, "\n")
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	removeCmd.Flags().String("reason", "", "Why the hook is removed; the archive entry keeps it")
}
