package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var scope string

var rootCmd = &cobra.Command{
	Use:   "steer",
	Short: "Review your agent's commits, then measure the fixes",
	Long: `steer reviews your agent's code before it commits, then helps you
prevent recurring mistakes.

Set up the commit gate:
  1. steer config set reviewer.provider claude-code   # claude-code | codex | agy | openai-compatible
  2. steer commit-gate install --harness claude-code --scope global
  3. steer status                                     # whether commits here are reviewed, and what to repair

Then read what the reviews taught you:
  steer dashboard         # the whole loop, interactive

Steer supports Claude Code and Codex on macOS and Linux, plus a Git pre-commit channel
(steer commit-gate install --precommit) for other workflows.`,
	Example: `  steer config set reviewer.provider claude-code
  steer commit-gate install --harness codex --scope global
  steer status
  steer dashboard`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		scope = strings.ToLower(strings.TrimSpace(scope))
		return claude.ValidateScope(scope)
	},
}

func Execute() error {
	requireKnownSubcommands(rootCmd)
	err := rootCmd.Execute()
	finishDump()
	if err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, styles.ErrorBox.Render(fmt.Sprintf("Error: %s", err.Error())))
		fmt.Fprintln(os.Stderr, styles.InfoText.Render(helpHint(os.Args[1:])))
		return err
	}
	return nil
}

// requireKnownSubcommands gives every command that only groups subcommands a
// RunE: an argument that names no subcommand is an error, and no argument shows
// the group's help. Cobra rejects an unknown subcommand only at the root; below
// it, a group without a RunE shows its help and exits 0.
func requireKnownSubcommands(c *cobra.Command) {
	if !c.Runnable() && c.HasSubCommands() {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		}
	}
	for _, sub := range c.Commands() {
		requireKnownSubcommands(sub)
	}
}

// helpHint names the help of the subcommand the arguments select, so an error on
// a deep subcommand does not send the reader to the root command's help. An
// unresolvable argument list falls back to the root.
func helpHint(args []string) string {
	path := rootCmd.CommandPath()
	if cmd, _, err := rootCmd.Find(args); err == nil && cmd != nil {
		path = cmd.CommandPath()
	}
	return fmt.Sprintf("Run `%s --help` for usage.", path)
}

// prepareState migrates the evidence corpora into the neutral root. A writing
// entry point calls it before it resolves any state path, so nothing in the same
// process holds a path from the root the migration just left.
func prepareState() error { return statestore.PrepareAll() }

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
	examples := dedent(cmd.Example)
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

// blankLine reports a line that holds nothing but whitespace.
func blankLine(s string) bool { return strings.TrimSpace(s) == "" }

// dedent drops the blank lines around an example block and removes the indent its
// remaining lines share. Trimming the block as a whole stripped the first line's
// indent only, which left the first example flush left and the rest indented.
func dedent(block string) string {
	lines := strings.Split(block, "\n")
	for len(lines) > 0 && blankLine(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blankLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	indent := -1
	for _, line := range lines {
		if blankLine(line) {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, line := range lines {
		switch {
		case blankLine(line):
			lines[i] = ""
		case indent > 0 && len(line) >= indent:
			lines[i] = line[indent:]
		}
	}
	return strings.Join(lines, "\n")
}
