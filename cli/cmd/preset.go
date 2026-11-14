package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var (
	presetSyncDryRun       bool
	presetSyncStrict       bool
	presetSyncPruneOrphans bool
)

var presetCmd = &cobra.Command{
	Use:   "preset",
	Short: "Remove the preset guards an earlier steer installed",
	Long: `This steer ships no presets. A guard an earlier steer installed from one keeps
running until you remove it: list names them, disable <name> removes one and
sync --prune-orphans removes all of them, archiving each.`,
}

// presetScopeFlag is the flag a printed command needs to reach this scope's store.
func presetScopeFlag(scope string) string {
	if scope == claude.GlobalScope {
		return " --scope global"
	}
	return ""
}

var presetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the preset guards an earlier steer installed",
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		orphans, err := presets.Orphans(scope)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📦 Presets"))
		fmt.Println()
		flag := presetScopeFlag(scope)
		if len(orphans) == 0 {
			fmt.Println(styles.InfoBox.Render("This Steer ships no presets, and the " + scope + " scope holds no guard an earlier Steer installed from one."))
			return nil
		}
		fmt.Println(styles.SectionHeader.Render(fmt.Sprintf("%d guard(s) from presets this Steer no longer ships", len(orphans))))
		for _, orphan := range orphans {
			fmt.Printf("  %s (preset %s)\n", orphan.HookName, orphanSourceID(orphan))
		}
		fmt.Println()
		fmt.Println(styles.InfoBox.Render("Each keeps running until you remove it:\n" +
			"`steer preset disable <name>" + flag + "` removes one, and\n" +
			"`steer preset sync --prune-orphans" + flag + "` removes all of them."))
		return nil
	},
}

func orphanSourceID(orphan presets.Orphan) string {
	if orphan.SourceID == "" {
		return "(empty)"
	}
	return orphan.SourceID
}

var presetDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Remove one guard an earlier steer installed from a preset",
	Long: `disable removes a guard an earlier steer installed from a preset, named by its
id or its preset name, and archives it. Every such guard's block names this command.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		targets, err := currentRegistrars(scope)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(args[0])
		found, err := presets.DisableOrphan(scope, name, targets...)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("no guard from preset %s in the %s scope", name, scope)
		}
		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf("Preset: %s\nStatus: removed (archived)\nScope: %s", name, scope)))
		return nil
	},
}

var presetSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Report, or with --prune-orphans remove, the guards from presets steer no longer ships",
	Example: `  steer preset sync
  steer preset sync --strict
  steer preset sync --prune-orphans --dry-run
  steer preset sync --prune-orphans`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		targets, err := currentRegistrars(scope)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🔄 Sync Presets"))
		fmt.Println()

		var orphans []presets.Orphan
		var syncErr error
		if presetSyncPruneOrphans {
			orphans, syncErr = presets.PruneOrphans(scope, presetSyncDryRun, targets...)
		} else {
			orphans, syncErr = presets.Orphans(scope)
		}
		kept, pruned := len(orphans), 0
		if presetSyncPruneOrphans {
			kept, pruned = 0, len(orphans)
		}
		summary := fmt.Sprintf("Scope: %s\nOrphans kept: %d\nOrphans pruned: %d", scope, kept, pruned)
		if presetSyncDryRun {
			summary += fmt.Sprintf("\nDry-run planned mutations: %d", pruned)
		}
		if syncErr != nil {
			fmt.Println(styles.WarningBox.Render(summary))
		} else {
			fmt.Println(styles.SuccessBox.Render(summary))
		}
		if len(orphans) > 0 {
			fmt.Println()
			fmt.Println(styles.SectionHeader.Render("Orphan Factory Entries"))
			for _, orphan := range orphans {
				fmt.Println(styles.WarningText.Render(fmt.Sprintf("- hook=%s source_id=%s", orphan.HookName, orphanSourceID(orphan))))
			}
		}
		if syncErr != nil {
			return syncErr
		}
		if presetSyncStrict && kept > 0 {
			return fmt.Errorf("strict mode: found %d orphan factory entries", kept)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(presetCmd)

	presetCmd.AddCommand(presetListCmd)
	presetCmd.AddCommand(presetDisableCmd)
	presetCmd.AddCommand(presetSyncCmd)

	presetSyncCmd.Flags().BoolVar(&presetSyncDryRun, "dry-run", false, "With --prune-orphans, name what would be removed without removing it")
	presetSyncCmd.Flags().BoolVar(&presetSyncStrict, "strict", false, "Return non-zero when orphan factory entries are kept")
	presetSyncCmd.Flags().BoolVar(&presetSyncPruneOrphans, "prune-orphans", false, "Archive and remove every orphan factory entry")
}
