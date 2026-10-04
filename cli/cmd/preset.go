package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var (
	presetListEnabledOnly  bool
	presetListBundle       string
	presetListVerbose      bool
	presetSyncDryRun       bool
	presetSyncStrict       bool
	presetSyncPruneOrphans bool
)

var presetCmd = &cobra.Command{
	Use:   "preset",
	Short: "Manage bundled factory presets",
	Long: `Preset manages bundled factory hooks shipped with steer.
Presets are disabled by default and can be enabled individually or by bundle.`,
}

var presetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available factory presets",
	Example: `  steer preset list
  steer preset list --bundle security
  steer preset list --enabled-only --verbose`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📦 Factory Presets"))
		fmt.Println()

		scope := currentScope()

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}

		items, err := catalog.ListVisible(presetListBundle)
		if err != nil {
			return err
		}

		manifest, err := claude.LoadManifest(scope)
		if err != nil {
			return err
		}

		type row struct {
			item    presets.CatalogItem
			enabled bool
			entry   claude.ManifestEntry
		}

		rows := make([]row, 0, len(items))
		for _, item := range items {
			hookName := strings.TrimSuffix(item.HookFile, ".py")
			entry, ok := manifest.Hooks[hookName]
			enabled := ok && strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name
			if presetListEnabledOnly && !enabled {
				continue
			}
			rows = append(rows, row{
				item:    item,
				enabled: enabled,
				entry:   entry,
			})
		}

		if len(rows) == 0 {
			fmt.Println(styles.InfoBox.Render("No presets matched the current filters."))
			return nil
		}

		sort.Slice(rows, func(i, j int) bool {
			return rows[i].item.Name < rows[j].item.Name
		})

		fmt.Println(styles.SectionHeader.Render(fmt.Sprintf("Found %d presets", len(rows))))
		fmt.Println()
		fmt.Printf("%-40s %-8s %-10s %s\n", "NAME", "STATUS", "BUNDLE", "DESCRIPTION")
		for _, row := range rows {
			status := "disabled"
			if row.enabled {
				status = "enabled"
			}
			fmt.Printf("%-40s %-8s %-10s %s\n", row.item.Name, status, presets.BundleFor(row.item), row.item.Description)
			if !presetListVerbose {
				continue
			}

			source := claude.SourceFactory
			promptVersion := row.item.PromptVersion
			hookVersion := row.item.HookVersion
			updated := "-"
			model := "-"
			if row.enabled {
				if strings.TrimSpace(row.entry.Source) != "" {
					source = row.entry.Source
				}
				if strings.TrimSpace(row.entry.PromptVersion) != "" {
					promptVersion = row.entry.PromptVersion
				}
				if strings.TrimSpace(row.entry.HookVersion) != "" {
					hookVersion = row.entry.HookVersion
				}
				updated = strings.TrimSpace(row.entry.Updated)
				if updated == "" {
					updated = strings.TrimSpace(row.entry.Created)
				}
				if updated == "" {
					updated = "-"
				}
				if strings.TrimSpace(row.entry.Model) != "" {
					model = row.entry.Model
				}
			}
			fmt.Printf("  meta: source=%s prompt_version=%s hook_version=%s updated=%s model=%s\n", source, promptVersion, hookVersion, updated, model)
		}

		return nil
	},
}

var presetEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable one factory preset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("✅ Enable Preset"))
		fmt.Println()

		scope := currentScope()
		force, _ := cmd.Flags().GetBool("force")
		name := strings.TrimSpace(args[0])

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}
		item, ok := presets.FindVisiblePreset(catalog, name)
		if !ok {
			return fmt.Errorf("preset not found: %s", name)
		}

		status, err := presets.EnablePreset(scope, catalog, item, force, targets...)
		if err != nil {
			return err
		}

		ensuredCount, err := presets.EnsureAutoManagedFactoryPresets(scope, catalog, false, targets...)
		if err != nil {
			return fmt.Errorf("enabled preset %s but failed to ensure internal factory presets: %w", name, err)
		}

		message := fmt.Sprintf("Preset: %s\nStatus: %s\nScope: %s", name, status, scope)
		if ensuredCount > 0 {
			message += fmt.Sprintf("\nInternal auto-managed ensured: %d", ensuredCount)
		}
		fmt.Println(styles.SuccessBox.Render(message))
		return nil
	},
}

var presetDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable one factory preset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🗑️  Disable Preset"))
		fmt.Println()

		scope := currentScope()
		force, _ := cmd.Flags().GetBool("force")
		name := strings.TrimSpace(args[0])

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}
		// An internal preset is never offered, but its block names this command as
		// the user's way to remove it, so disable accepts one.
		item, ok := catalog.Find(name)
		if !ok {
			return fmt.Errorf("preset not found: %s", name)
		}

		status, err := presets.DisablePreset(scope, item, force, targets...)
		if err != nil {
			return err
		}

		message := fmt.Sprintf("Preset: %s\nStatus: %s\nScope: %s", name, status, scope)
		fmt.Println(styles.SuccessBox.Render(message))
		return nil
	},
}

var presetEnableBundleCmd = &cobra.Command{
	Use:   "enable-bundle <bundle>",
	Short: "Enable all presets in a bundle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		scope := currentScope()
		force, _ := cmd.Flags().GetBool("force")
		bundle := strings.TrimSpace(args[0])

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📚 Enable Preset Bundle"))
		fmt.Println()

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}
		names, err := catalog.BundleMembers(bundle)
		if err != nil {
			return err
		}
		sort.Strings(names)

		enabledCount := 0
		alreadyCount := 0
		failureCount := 0

		for _, name := range names {
			item, ok := presets.FindVisiblePreset(catalog, name)
			if !ok {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: preset missing from visible catalog map", name)))
				continue
			}
			utils.Working(fmt.Sprintf("Enabling %s...", name))
			status, err := presets.EnablePreset(scope, catalog, item, force, targets...)
			if err != nil {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: %s", name, err)))
				continue
			}
			if status == "enabled" || status == "enabled (overwrote existing hook)" {
				enabledCount++
			} else {
				alreadyCount++
			}
			fmt.Println(styles.SuccessText.Render(fmt.Sprintf("✓ %s: %s", name, status)))
		}

		internalEnsured, err := presets.EnsureAutoManagedFactoryPresets(scope, catalog, false, targets...)
		if err != nil {
			failureCount++
			fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ internal auto-managed ensure failed: %s", err)))
		}

		fmt.Println()
		fmt.Println(styles.InfoBox.Render(fmt.Sprintf(
			"Bundle: %s\nEnabled: %d\nAlready enabled: %d\nInternal auto-managed ensured: %d\nFailed: %d",
			bundle,
			enabledCount,
			alreadyCount,
			internalEnsured,
			failureCount,
		)))

		if failureCount > 0 {
			return fmt.Errorf("bundle enable completed with %d failures", failureCount)
		}
		return nil
	},
}

var presetDisableBundleCmd = &cobra.Command{
	Use:   "disable-bundle <bundle>",
	Short: "Disable all presets in a bundle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		scope := currentScope()
		force, _ := cmd.Flags().GetBool("force")
		bundle := strings.TrimSpace(args[0])

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📚 Disable Preset Bundle"))
		fmt.Println()

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}
		names, err := catalog.BundleMembers(bundle)
		if err != nil {
			return err
		}
		sort.Strings(names)

		disabledCount := 0
		alreadyCount := 0
		failureCount := 0

		for _, name := range names {
			item, ok := presets.FindVisiblePreset(catalog, name)
			if !ok {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: preset missing from visible catalog map", name)))
				continue
			}
			utils.Working(fmt.Sprintf("Disabling %s...", name))
			status, err := presets.DisablePreset(scope, item, force, targets...)
			if err != nil {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: %s", name, err)))
				continue
			}
			if status == "disabled" {
				disabledCount++
			} else {
				alreadyCount++
			}
			fmt.Println(styles.SuccessText.Render(fmt.Sprintf("✓ %s: %s", name, status)))
		}

		fmt.Println()
		fmt.Println(styles.InfoBox.Render(fmt.Sprintf(
			"Bundle: %s\nDisabled: %d\nAlready disabled: %d\nFailed: %d",
			bundle,
			disabledCount,
			alreadyCount,
			failureCount,
		)))

		if failureCount > 0 {
			return fmt.Errorf("bundle disable completed with %d failures", failureCount)
		}
		return nil
	},
}

var presetSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Reconcile enabled factory presets with the embedded catalog",
	Example: `  steer preset sync
  steer preset sync --dry-run
  steer preset sync --strict
  steer preset sync --prune-orphans`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🔄 Sync Presets"))
		fmt.Println()

		scope := currentScope()

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}

		result, syncErr := presets.SyncFactoryPresets(scope, catalog, presets.SyncOptions{
			DryRun:       presetSyncDryRun,
			Strict:       presetSyncStrict,
			PruneOrphans: presetSyncPruneOrphans,
		}, targets...)
		if result == nil {
			result = &presets.SyncResult{}
		}

		summary := fmt.Sprintf(
			"Scope: %s\nChecked factory presets: %d\nUp-to-date: %d\nReinstalled: %d\nMetadata drift: %d\nMissing file: %d\nContent drift: %d\nInternal ensured: %d\nOrphans kept: %d\nOrphans pruned: %d",
			scope,
			result.CheckedFactory,
			result.UpToDate,
			result.Reinstalled,
			result.MetadataDrift,
			result.MissingFile,
			result.ContentDrift,
			result.InternalEnsured,
			result.OrphansKept,
			result.OrphansPruned,
		)
		if presetSyncDryRun {
			summary += fmt.Sprintf("\nDry-run planned mutations: %d", result.DryRunPlanned)
		}

		if syncErr != nil {
			fmt.Println(styles.WarningBox.Render(summary))
		} else {
			fmt.Println(styles.SuccessBox.Render(summary))
		}

		if len(result.Orphans) > 0 {
			fmt.Println()
			fmt.Println(styles.SectionHeader.Render("Orphan Factory Entries"))
			for _, orphan := range result.Orphans {
				sourceID := orphan.SourceID
				if strings.TrimSpace(sourceID) == "" {
					sourceID = "(empty)"
				}
				fmt.Println(styles.WarningText.Render(fmt.Sprintf("- hook=%s source_id=%s", orphan.HookName, sourceID)))
			}
		}

		if syncErr != nil {
			return syncErr
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(presetCmd)

	presetCmd.AddCommand(presetListCmd)
	presetCmd.AddCommand(presetEnableCmd)
	presetCmd.AddCommand(presetDisableCmd)
	presetCmd.AddCommand(presetEnableBundleCmd)
	presetCmd.AddCommand(presetDisableBundleCmd)
	presetCmd.AddCommand(presetSyncCmd)

	presetListCmd.Flags().BoolVar(&presetListEnabledOnly, "enabled-only", false, "Show only enabled presets")
	presetListCmd.Flags().StringVar(&presetListBundle, "bundle", "", "Filter by bundle (security|quality)")
	presetListCmd.Flags().BoolVar(&presetListVerbose, "verbose", false, "Include metadata columns")

	presetEnableCmd.Flags().Bool("force", false, "Overwrite conflicting existing hook")
	presetDisableCmd.Flags().Bool("force", false, "Remove even when ownership metadata conflicts")
	presetEnableBundleCmd.Flags().Bool("force", false, "Overwrite conflicting existing hooks")
	presetDisableBundleCmd.Flags().Bool("force", false, "Remove hooks even when ownership metadata conflicts")

	presetSyncCmd.Flags().BoolVar(&presetSyncDryRun, "dry-run", false, "Preview reconcile actions without mutating files")
	presetSyncCmd.Flags().BoolVar(&presetSyncStrict, "strict", false, "Return non-zero when orphan factory entries are kept")
	presetSyncCmd.Flags().BoolVar(&presetSyncPruneOrphans, "prune-orphans", false, "Remove orphan factory entries via managed lifecycle")
}
