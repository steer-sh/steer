package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/tier"
)

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Manage instruction rules — prose Steer writes into every agent's instruction document",
	Long: `An instruction rule is written guidance with no guard behind it. It lives in
the shared store's steer-manifest.json beside the guards, and every target harness
derives its ` + "`## Steer Rules`" + ` section from that one file.

The rule is the artifact an instruction-tier intervention names, so it carries a
digest a later read can recompute. Steer owns the section and nothing outside it:
prose written elsewhere in CLAUDE.md or AGENTS.md moves nothing Steer reads.`,
}

var rulesAddCmd = &cobra.Command{
	Use:     "add <rule-id> --rule <text>",
	Short:   "Write an instruction rule into the store and every agent's document",
	Example: `  steer rules add config-path --rule "resolve a config path from the repo root, not the cwd"`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text, _ := cmd.Flags().GetString("rule")
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		scope := currentScope()
		// The store's rules are printed before the write. Only an identical
		// normalized duplicate is refused mechanically, so prose that already says
		// this in other words is caught by the reader or not at all.
		if existing, err := claude.ListManagedRules(scope); err == nil && len(existing) > 0 {
			fmt.Printf("Rules already in the %s store:\n", scope)
			for _, rule := range existing {
				fmt.Printf("  %s  %s\n", rule.ID, rule.Text)
			}
			fmt.Println()
		}
		result, err := claude.InstallManagedRule(scope, args[0], text, targets...)
		if err != nil {
			return err
		}
		verb := "Added rule"
		if result.Overwritten {
			verb = "Reworded rule"
		}
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"%s: %s\nScope: %s\nHarness: %s", verb, result.ID, scope, harnessNames(targets))))
		if result.Overwritten {
			fmt.Printf("  was: %s\n", result.PreviousText)
		}
		return nil
	},
}

var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the instruction rules in this scope's store",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		rules, err := claude.ListManagedRules(scope)
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			fmt.Printf("No instruction rules in the %s store.\n", scope)
			return nil
		}
		for _, rule := range rules {
			// The digest is recomputed over the stored text, never read from the
			// entry's own stamp, for the reason mine.SteerRuleFacts recomputes: a
			// hand-edited manifest must not make a reader name text the store does
			// not hold. It is also the only form guaranteed long enough to trim.
			fmt.Printf("%s  %s\n", rule.ID, rule.Text)
			fmt.Printf("  rule_hash %s  created %s\n",
				claude.HashRule(rule.Text)[:16], rule.Created)
		}
		return nil
	},
}

var rulesRemoveCmd = &cobra.Command{
	Use:   "remove <rule-id>",
	Short: "Delete an instruction rule from the store and every agent's document",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		scope := currentScope()
		reason, _ := cmd.Flags().GetString("reason")
		result, err := claude.RemoveManagedRule(scope, args[0], strings.TrimSpace(reason), targets...)
		if errors.Is(err, claude.ErrRuleNotFound) {
			return fmt.Errorf("%w\n%s", err, otherTierRemoveHint(tier.Instruction, args[0], scope))
		}
		if err != nil {
			return err
		}
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Removed rule: %s\nScope: %s\nHarness: %s\nArchived: %s", result.ID, scope, harnessNames(targets), result.ArchivePath)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rulesCmd)
	rulesCmd.AddCommand(rulesAddCmd, rulesListCmd, rulesRemoveCmd)
	rulesAddCmd.Flags().String("rule", "", "the rule text the agent reads (required)")
	_ = rulesAddCmd.MarkFlagRequired("rule")
	rulesRemoveCmd.Flags().String("reason", "", "why the rule is removed; the archive entry keeps it")
}
