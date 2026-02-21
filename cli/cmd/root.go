package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var scope string

var rootCmd = &cobra.Command{
	Use:   "steer",
	Short: "AI Coding Memory System - Never make the same mistake twice",
	Long: `steer is a CLI tool that consolidates and manages Claude Code hooks generated
from steer rules.

Workflow:
  1. steer register <hook.py> --rule "your rule"   # Register hook + consolidate
  2. steer test <hook-name> --code "sample code"   # Test locally (optional)
  3. steer list                                     # Review active hooks
  4. steer verify --staged                          # Verify staged changes before commit
  5. steer git-hooks install                        # Install pre-commit enforcement
  6. steer remove <hook-name>                       # Remove when no longer needed`,
	Example: `  steer register .claude/hooks/no-var-hook.py --rule "never use var | use const or let"
  steer test no-var-hook --code "var x = 1"
  steer list
  steer verify --staged
  steer git-hooks install
  steer remove no-var-hook --force`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		scope = strings.ToLower(strings.TrimSpace(scope))
		return claude.ValidateScope(scope)
	},
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, styles.ErrorBox.Render(fmt.Sprintf("Error: %s", err.Error())))
		fmt.Fprintln(os.Stderr, styles.InfoText.Render("Run `steer --help` for usage."))
		return err
	}
	return nil
}

func currentScope() string {
	if scope == "" {
		return claude.ProjectScope
	}
	return scope
}

func init() {
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		renderHelp(cmd)
	})
	rootCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		renderHelp(cmd)
		return nil
	})
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		renderHelp(cmd)
		return err
	})

	rootCmd.PersistentFlags().StringVar(
		&scope,
		"scope",
		claude.ProjectScope,
		fmt.Sprintf("Scope for hook operations (%s|%s)", claude.ProjectScope, claude.GlobalScope),
	)
}

func renderHelp(cmd *cobra.Command) {
	fmt.Println()
	fmt.Println(styles.MainHeader.Render(styles.Logo() + " CLI"))
	fmt.Println()

	description := strings.TrimSpace(cmd.Long)
	if description == "" {
		description = strings.TrimSpace(cmd.Short)
	}
	if description != "" {
		fmt.Println(styles.InfoBox.Render(description))
		fmt.Println()
	}

	fmt.Println(styles.SectionHeader.Render("Usage"))
	fmt.Println(styles.CodeBlock.Render(helpUsageLine(cmd)))
	fmt.Println()

	printExamples(cmd)
	printCommands(cmd)
	printFlags(cmd)

	if cmd.HasAvailableSubCommands() {
		fmt.Println()
		fmt.Println(styles.MutedText.Render(`Use "steer [command] --help" for more information about a command.`))
	}
}

func printExamples(cmd *cobra.Command) {
	examples := strings.TrimSpace(cmd.Example)
	if examples == "" {
		return
	}

	fmt.Println(styles.SectionHeader.Render("Examples"))
	fmt.Println(styles.CodeBlock.Render(examples))
	fmt.Println()
}

func printCommands(cmd *cobra.Command) {
	commands := cmd.Commands()
	hasCommands := false
	for _, c := range commands {
		if c.Hidden || !c.IsAvailableCommand() {
			continue
		}
		hasCommands = true
		break
	}
	if !hasCommands {
		return
	}

	fmt.Println(styles.SectionHeader.Render("Commands"))
	for _, c := range commands {
		if c.Hidden || !c.IsAvailableCommand() {
			continue
		}
		fmt.Printf("  %s\t%s\n", styles.Bold.Render(c.Name()), c.Short)
	}
	fmt.Println()
}

func printFlags(cmd *cobra.Command) {
	localFlags := strings.TrimSpace(cmd.LocalFlags().FlagUsagesWrapped(100))
	inheritedFlags := strings.TrimSpace(cmd.InheritedFlags().FlagUsagesWrapped(100))

	if localFlags == "" && inheritedFlags == "" {
		return
	}

	fmt.Println(styles.SectionHeader.Render("Flags"))
	if localFlags != "" {
		fmt.Println(styles.CodeBlock.Render(localFlags))
	}
	if inheritedFlags != "" {
		fmt.Println()
		fmt.Println(styles.MutedText.Render("Inherited:"))
		fmt.Println(styles.CodeBlock.Render(inheritedFlags))
	}
}

func helpUsageLine(cmd *cobra.Command) string {
	if cmd.Parent() == nil {
		return "steer [command]"
	}
	return cmd.UseLine()
}
