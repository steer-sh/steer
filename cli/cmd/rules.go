package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
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
prose written elsewhere in CLAUDE.md or AGENTS.md moves nothing Steer reads.

A reviewed rule is an instruction rule that the commit gate also gives to its
reviewer, for a commit that changes a path the rule names. Use it for a rule whose
violation shows in the diff or the commit message. A path is ` + "`dir/`" + `,
` + "`*.ext`" + `, ` + "`dir/*.ext`" + ` or one file, relative to the repository root. A
project reviewed rule reaches the reviewer only once the store's
steer-manifest.json is committed, because the gate reads project rules from HEAD.`,
}

var rulesAddCmd = &cobra.Command{
	Use:   "add <rule-id> --rule <text>",
	Short: "Write an instruction rule into the store and every agent's document",
	Example: `  steer rules add config-path --rule "resolve a config path from the repo root, not the cwd"
  steer rules add tui-reads-status --rule "never re-derive status health in tui | read it from status" \
    --review-path cli/internal/tui/`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text, _ := cmd.Flags().GetString("rule")
		opts, err := ruleOptionsFromFlags(cmd)
		if err != nil {
			return err
		}
		targets, err := currentRegistrars(currentScope())
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
		result, err := claude.InstallRule(scope, args[0], text, opts, targets...)
		if err != nil {
			return err
		}
		verb := "Added rule"
		if result.Overwritten {
			verb = "Reworded rule"
		}
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"%s: %s\nScope: %s\nHarness: %s", verb, result.ID, scope, harnessNames(targets))))
		if result.Overwritten && result.PreviousText != strings.TrimSpace(text) {
			fmt.Printf("  was: %s\n", result.PreviousText)
		}
		if result.Review != nil {
			fmt.Printf("Reviewed: %s\n", reviewPathsPhrase(result.Review))
		}
		printReviewedRuleCommitNote(os.Stdout, scope, result.ID, result.Review != nil)
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
			if rule.Review != nil {
				fmt.Printf("  reviewed: %s\n", reviewPathsPhrase(rule.Review))
			}
		}
		return nil
	},
}

var rulesRemoveCmd = &cobra.Command{
	Use:   "remove <rule-id>",
	Short: "Delete an instruction rule from the store and every agent's document",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars(currentScope())
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
		printReviewedRuleCommitNote(os.Stdout, scope, result.ID, false)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rulesCmd)
	rulesCmd.AddCommand(rulesAddCmd, rulesListCmd, rulesRemoveCmd)
	rulesAddCmd.Flags().String("rule", "", "the rule text the agent reads (required)")
	rulesAddCmd.Flags().StringArray("review-path", nil,
		"make it a reviewed rule for commits that change this path (repeatable)")
	rulesAddCmd.Flags().Bool("review", false,
		"make it a reviewed rule for every commit; only a global rule may name no --review-path")
	rulesAddCmd.Flags().Bool("no-review", false, "make a reviewed rule an instruction rule again")
	rulesAddCmd.MarkFlagsMutuallyExclusive("review-path", "no-review")
	rulesAddCmd.MarkFlagsMutuallyExclusive("review", "no-review")
	_ = rulesAddCmd.MarkFlagRequired("rule")
	rulesRemoveCmd.Flags().String("reason", "", "why the rule is removed; the archive entry keeps it")
}

// ruleOptionsFromFlags reads the review flags of `steer rules add`.
func ruleOptionsFromFlags(cmd *cobra.Command) (claude.RuleOptions, error) {
	paths, _ := cmd.Flags().GetStringArray("review-path")
	review, _ := cmd.Flags().GetBool("review")
	clear, _ := cmd.Flags().GetBool("no-review")
	switch {
	case clear:
		return claude.RuleOptions{ClearReview: true}, nil
	case review || len(paths) > 0:
		return claude.RuleOptions{Review: &claude.RuleReview{Paths: paths}}, nil
	}
	return claude.RuleOptions{}, nil
}

// reviewPathsPhrase names the commits a reviewed rule reaches.
func reviewPathsPhrase(r *claude.RuleReview) string {
	if len(r.Paths) == 0 {
		return "every commit"
	}
	return "commits that change " + strings.Join(r.Paths, ", ")
}

// printReviewedRuleCommitNote tells the user that a project reviewed rule's change
// reaches the commit reviewer only once the store's manifest is committed, because the
// gate reads project rules from HEAD. reviewed is whether the rule is reviewed after
// the change. A rule that is not gets the note only while HEAD holds it as reviewed,
// because only then does the reviewer go on reading it.
func printReviewedRuleCommitNote(w io.Writer, scope, id string, reviewed bool) {
	if scope != claude.ProjectScope {
		return
	}
	switch {
	case reviewed:
		fmt.Fprintf(w, "The commit gate reads project rules from HEAD. Commit %s on its own, before the change "+
			"the rule is for: a commit that carries both is reviewed against the rule as HEAD holds it.\n", projectManifestPath())
	case gate.LoadAddendum(repoRootOrNone()).HoldsRule(claude.ProjectScope, id):
		fmt.Fprintf(w, "The commit gate reads project rules from HEAD, so its reviewer goes on reading this "+
			"rule until you commit %s.\n", projectManifestPath())
	}
}
