package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered individual hooks",
	Long: `List displays individual steer-managed hooks by scanning the hooks directory
for .py files (excluding steer-hooks.py). It reads rule text from
steer-manifest.json and does not rely on settings.json entries.`,
	Example: `  steer list
  steer list --scope global`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📋 Registered Hooks"))
		fmt.Println()

		scope := currentScope()

		hooks, err := claude.ListIndividualHooks(scope)
		if err != nil {
			return err
		}
		if len(hooks) == 0 {
			fmt.Println(styles.InfoBox.Render(fmt.Sprintf("No hooks found in %s scope.", scope)))
			return nil
		}

		manifest, err := claude.LoadManifest(scope)
		if err != nil {
			return err
		}

		sort.Slice(hooks, func(i, j int) bool {
			return hooks[i].Name < hooks[j].Name
		})

		fmt.Println(styles.SectionHeader.Render(fmt.Sprintf("Found %d hooks in %s scope", len(hooks), scope)))
		fmt.Println()

		for i, hook := range hooks {
			rule := ""
			if entry, ok := manifest.Hooks[hook.Name]; ok {
				rule = entry.Rule
			}
			if rule == "" {
				rule = "(no rule recorded)"
			}

			fmt.Println(styles.HookListItemWithRule(i+1, hook.Name, hook.Path, rule))
			if i < len(hooks)-1 {
				fmt.Println()
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
