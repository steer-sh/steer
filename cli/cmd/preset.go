package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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

type presetSyncOptions struct {
	DryRun       bool
	Strict       bool
	PruneOrphans bool
}

type presetSyncOrphan struct {
	HookName string
	SourceID string
}

type presetSyncResult struct {
	CheckedFactory  int
	UpToDate        int
	Reinstalled     int
	MetadataDrift   int
	MissingFile     int
	ContentDrift    int
	InternalEnsured int
	DryRunPlanned   int
	OrphansKept     int
	OrphansPruned   int
	Orphans         []presetSyncOrphan
}

const (
	presetDriftMetadata    = "metadata"
	presetDriftMissingFile = "missing_file"
	presetDriftContent     = "content"
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
		item, ok := findVisiblePreset(catalog, name)
		if !ok {
			return fmt.Errorf("preset not found: %s", name)
		}

		status, err := enablePreset(scope, catalog, item, force)
		if err != nil {
			return err
		}

		ensuredCount, err := ensureAutoManagedFactoryPresets(scope, catalog, false)
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
		item, ok := findVisiblePreset(catalog, name)
		if !ok {
			return fmt.Errorf("preset not found: %s", name)
		}

		status, err := disablePreset(scope, item, force)
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
			item, ok := findVisiblePreset(catalog, name)
			if !ok {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: preset missing from visible catalog map", name)))
				continue
			}
			utils.Working(fmt.Sprintf("Enabling %s...", name))
			status, err := enablePreset(scope, catalog, item, force)
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

		internalEnsured, err := ensureAutoManagedFactoryPresets(scope, catalog, false)
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
			item, ok := findVisiblePreset(catalog, name)
			if !ok {
				failureCount++
				fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ %s: preset missing from visible catalog map", name)))
				continue
			}
			utils.Working(fmt.Sprintf("Disabling %s...", name))
			status, err := disablePreset(scope, item, force)
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
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🔄 Sync Presets"))
		fmt.Println()

		scope := currentScope()

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}

		result, syncErr := syncFactoryPresets(scope, catalog, presetSyncOptions{
			DryRun:       presetSyncDryRun,
			Strict:       presetSyncStrict,
			PruneOrphans: presetSyncPruneOrphans,
		})
		if result == nil {
			result = &presetSyncResult{}
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

func findVisiblePreset(catalog *presets.Catalog, name string) (presets.CatalogItem, bool) {
	item, ok := catalog.Find(strings.TrimSpace(name))
	if !ok || item.Internal {
		return presets.CatalogItem{}, false
	}
	return item, true
}

func enablePreset(scope string, catalog *presets.Catalog, item presets.CatalogItem, force bool) (string, error) {
	hookName := strings.TrimSuffix(item.HookFile, ".py")
	if hookName == "" {
		return "", fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	targetPath := filepath.Join(hooksDir, item.HookFile)
	installForce := force

	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return "", err
	}

	if existing, ok := manifest.Hooks[hookName]; ok {
		isFactoryMatch := strings.EqualFold(strings.TrimSpace(existing.Source), claude.SourceFactory) && strings.TrimSpace(existing.SourceID) == item.Name
		if isFactoryMatch {
			if _, err := os.Stat(targetPath); err == nil {
				return "already enabled", nil
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to check hook file %s: %w", targetPath, err)
			}
			installForce = true
		}
		if !force {
			return "", fmt.Errorf("conflict: hook %s already exists with source=%s source_id=%s (use --force to overwrite)", hookName, existing.Source, existing.SourceID)
		}
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !installForce {
			return "", fmt.Errorf("conflict: hook file already exists at %s (use --force to overwrite)", targetPath)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to check hook file %s: %w", targetPath, err)
	}

	result, err := installFactoryPreset(scope, catalog, item, installForce)
	if err != nil {
		return "", err
	}
	if result.Overwritten {
		return "enabled (overwrote existing hook)", nil
	}
	return "enabled", nil
}

func installFactoryPreset(scope string, catalog *presets.Catalog, item presets.CatalogItem, force bool) (*claude.InstallManagedHookResult, error) {
	hookName := strings.TrimSuffix(item.HookFile, ".py")
	if hookName == "" {
		return nil, fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	content, err := catalog.ReadHookContent(item)
	if err != nil {
		return nil, err
	}

	result, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: hookName,
		FileName: item.HookFile,
		Rule:     item.Rule,
		Content:  content,
		Force:    force,
		Metadata: claude.HookInstallMetadata{
			Source:        claude.SourceFactory,
			SourceID:      item.Name,
			SourceVersion: catalog.CatalogVersion,
			PromptVersion: item.PromptVersion,
			PromptModules: item.PromptModules,
			HookVersion:   item.HookVersion,
		},
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func disablePreset(scope string, item presets.CatalogItem, force bool) (string, error) {
	hookName := strings.TrimSuffix(item.HookFile, ".py")
	if hookName == "" {
		return "", fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return "", err
	}

	entry, hasEntry := manifest.Hooks[hookName]
	if hasEntry {
		isFactoryMatch := strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name
		if !isFactoryMatch && !force {
			return "", fmt.Errorf("conflict: hook %s is not managed by preset %s (source=%s source_id=%s). use --force to remove anyway", hookName, item.Name, entry.Source, entry.SourceID)
		}
	}

	if !hasEntry {
		hooksDir, err := claude.GetHooksDir(scope)
		if err != nil {
			return "", err
		}
		targetPath := filepath.Join(hooksDir, item.HookFile)
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			return "already disabled", nil
		} else if err != nil {
			return "", fmt.Errorf("failed to check hook file %s: %w", targetPath, err)
		}
		if !force {
			return "", fmt.Errorf("hook file exists at %s but preset is not enabled in manifest (use --force to remove)", targetPath)
		}
	}

	if _, err := claude.RemoveManagedHook(scope, hookName); err != nil {
		return "", err
	}
	return "disabled", nil
}

func ensureAutoManagedFactoryPresets(scope string, catalog *presets.Catalog, dryRun bool) (int, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return 0, err
	}

	ensuredCount := 0
	for _, item := range catalog.InternalAutoEnabled() {
		hookName := strings.TrimSuffix(item.HookFile, ".py")
		if hookName == "" {
			return ensuredCount, fmt.Errorf("invalid preset hook_file for %s", item.Name)
		}

		entry, hasEntry := manifest.Hooks[hookName]
		needsInstall := true
		if hasEntry && strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name {
			var reason string
			needsInstall, reason, err = factoryPresetNeedsReinstall(scope, catalog, item, hookName, entry)
			if err != nil {
				return ensuredCount, err
			}
			_ = reason
		}

		if !needsInstall {
			continue
		}

		ensuredCount++
		if dryRun {
			continue
		}

		if _, err := installFactoryPreset(scope, catalog, item, true); err != nil {
			return ensuredCount, err
		}

		manifest, err = claude.LoadManifest(scope)
		if err != nil {
			return ensuredCount, err
		}
	}

	return ensuredCount, nil
}

func syncFactoryPresets(scope string, catalog *presets.Catalog, options presetSyncOptions) (*presetSyncResult, error) {
	result := &presetSyncResult{}

	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return result, err
	}

	factoryHookNames := make([]string, 0, len(manifest.Hooks))
	for hookName, entry := range manifest.Hooks {
		if !strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) {
			continue
		}
		factoryHookNames = append(factoryHookNames, hookName)
	}
	sort.Strings(factoryHookNames)

	ensuredInternal := map[string]struct{}{}
	for _, hookName := range factoryHookNames {
		entry := manifest.Hooks[hookName]
		sourceID := strings.TrimSpace(entry.SourceID)

		item, found := catalog.Find(sourceID)
		if sourceID == "" || !found {
			result.Orphans = append(result.Orphans, presetSyncOrphan{
				HookName: hookName,
				SourceID: sourceID,
			})

			if options.PruneOrphans {
				if options.DryRun {
					result.OrphansPruned++
					result.DryRunPlanned++
				} else {
					if _, err := claude.RemoveManagedHook(scope, hookName); err != nil {
						return result, err
					}
					result.OrphansPruned++
				}
			} else {
				result.OrphansKept++
			}
			continue
		}

		if item.Internal && item.AutoEnableWithFactory {
			ensuredInternal[item.Name] = struct{}{}
		}

		result.CheckedFactory++
		needsInstall, driftReason, err := factoryPresetNeedsReinstall(scope, catalog, item, hookName, entry)
		if err != nil {
			return result, err
		}
		if !needsInstall {
			result.UpToDate++
			continue
		}

		switch driftReason {
		case presetDriftMetadata:
			result.MetadataDrift++
		case presetDriftMissingFile:
			result.MissingFile++
		case presetDriftContent:
			result.ContentDrift++
		}

		if options.DryRun {
			result.Reinstalled++
			result.DryRunPlanned++
			continue
		}
		if _, err := installFactoryPreset(scope, catalog, item, true); err != nil {
			return result, err
		}
		result.Reinstalled++
	}

	for _, item := range catalog.InternalAutoEnabled() {
		if _, seen := ensuredInternal[item.Name]; seen {
			continue
		}
		result.InternalEnsured++
		if options.DryRun {
			result.Reinstalled++
			result.DryRunPlanned++
			continue
		}
		if _, err := installFactoryPreset(scope, catalog, item, true); err != nil {
			return result, err
		}
		result.Reinstalled++
	}

	if options.Strict && result.OrphansKept > 0 {
		return result, fmt.Errorf("strict mode: found %d orphan factory entries", result.OrphansKept)
	}

	return result, nil
}

func factoryPresetNeedsReinstall(scope string, catalog *presets.Catalog, item presets.CatalogItem, hookName string, entry claude.ManifestEntry) (bool, string, error) {
	expectedHookName := strings.TrimSuffix(item.HookFile, ".py")
	if expectedHookName == "" {
		return false, "", fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	if hookName != expectedHookName {
		return true, presetDriftMetadata, nil
	}
	if strings.TrimSpace(entry.SourceID) != item.Name {
		return true, presetDriftMetadata, nil
	}
	if strings.TrimSpace(entry.SourceVersion) != catalog.CatalogVersion {
		return true, presetDriftMetadata, nil
	}
	if strings.TrimSpace(entry.PromptVersion) != item.PromptVersion {
		return true, presetDriftMetadata, nil
	}
	if strings.TrimSpace(entry.HookVersion) != item.HookVersion {
		return true, presetDriftMetadata, nil
	}
	if file := strings.TrimSpace(entry.File); file != "" && file != item.HookFile {
		return true, presetDriftMetadata, nil
	}

	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		return false, "", err
	}
	targetPath := filepath.Join(hooksDir, item.HookFile)
	actual, err := os.ReadFile(targetPath)
	if os.IsNotExist(err) {
		return true, presetDriftMissingFile, nil
	}
	if err != nil {
		return false, "", fmt.Errorf("failed reading preset hook %s: %w", targetPath, err)
	}

	expected, err := expectedFactoryPresetContent(catalog, item)
	if err != nil {
		return false, "", err
	}

	if sha256Hex(actual) != sha256Hex(expected) {
		return true, presetDriftContent, nil
	}
	return false, "", nil
}

func expectedFactoryPresetContent(catalog *presets.Catalog, item presets.CatalogItem) ([]byte, error) {
	content, err := catalog.ReadHookContent(item)
	if err != nil {
		return nil, err
	}
	return claude.ApplyMetadataHeader(content, claude.SourceFactory, item.PromptVersion, item.HookVersion), nil
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
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
