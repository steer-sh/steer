package presets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// SyncOptions controls how SyncFactoryPresets reconciles the manifest.
type SyncOptions struct {
	DryRun       bool
	Strict       bool
	PruneOrphans bool
}

// SyncOrphan is a factory manifest entry whose source_id left the catalog.
type SyncOrphan struct {
	HookName string
	SourceID string
}

// SyncResult counts what SyncFactoryPresets found or changed.
type SyncResult struct {
	CheckedFactory  int
	UpToDate        int
	Reinstalled     int
	MetadataDrift   int
	MissingFile     int
	ContentDrift    int
	InternalEnsured int
	InternalRemoved int
	DryRunPlanned   int
	OrphansKept     int
	OrphansPruned   int
	Orphans         []SyncOrphan
}

// FindVisiblePreset returns the catalog item for name, unless it is internal.
func FindVisiblePreset(catalog *Catalog, name string) (CatalogItem, bool) {
	item, ok := catalog.Find(strings.TrimSpace(name))
	if !ok || item.Internal {
		return CatalogItem{}, false
	}
	return item, true
}

// EnablePreset installs one factory preset into the scope's managed hooks.
func EnablePreset(scope string, catalog *Catalog, item CatalogItem, force bool, targets ...harness.Registrar) (string, error) {
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
		if !isFactoryMatch {
			if !force {
				return "", fmt.Errorf("conflict: hook %s already exists with source=%s source_id=%s (use --force to overwrite)", hookName, existing.Source, existing.SourceID)
			}
		} else {
			if _, err := os.Stat(targetPath); err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to check hook file %s: %w", targetPath, err)
			} else if err == nil {
				// The store already holds this preset, and the store is shared. A
				// target harness that never got the registration or the rule still
				// needs the install to run for it.
				drifted, _, driftErr := claude.TargetDrift(scope, existing.Rule, targets)
				if driftErr != nil {
					return "", driftErr
				}
				if !drifted {
					return "already enabled", nil
				}
			}
			installForce = true
		}
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !installForce {
			return "", fmt.Errorf("conflict: hook file already exists at %s (use --force to overwrite)", targetPath)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to check hook file %s: %w", targetPath, err)
	}

	result, err := InstallFactoryPreset(scope, catalog, item, installForce, targets...)
	if err != nil {
		return "", err
	}
	if result.Overwritten {
		return "enabled (overwrote existing hook)", nil
	}
	return "enabled", nil
}

// DesiredHookFromCatalogItem builds the DesiredHook a catalog item installs.
func DesiredHookFromCatalogItem(catalog *Catalog, item CatalogItem) (claude.DesiredHook, error) {
	hookName := strings.TrimSuffix(item.HookFile, ".py")
	if hookName == "" {
		return claude.DesiredHook{}, fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	content, err := catalog.ReadHookContent(item)
	if err != nil {
		return claude.DesiredHook{}, err
	}

	return claude.DesiredHook{
		HookName: hookName,
		FileName: item.HookFile,
		Rule:     item.Rule,
		Content:  content,
		Metadata: claude.HookInstallMetadata{
			Source:        claude.SourceFactory,
			SourceID:      item.Name,
			SourceVersion: catalog.CatalogVersion,
			PromptVersion: item.PromptVersion,
			PromptModules: item.PromptModules,
			HookVersion:   item.HookVersion,
		},
	}, nil
}

// InstallFactoryPreset writes one factory preset through the managed lifecycle.
func InstallFactoryPreset(scope string, catalog *Catalog, item CatalogItem, force bool, targets ...harness.Registrar) (*claude.InstallManagedHookResult, error) {
	desired, err := DesiredHookFromCatalogItem(catalog, item)
	if err != nil {
		return nil, err
	}

	result, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: desired.HookName,
		FileName: desired.FileName,
		Rule:     desired.Rule,
		Content:  desired.Content,
		Metadata: desired.Metadata,
	}, targets...)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DisablePreset removes one factory preset from the scope's managed hooks. When it
// removes the last visible factory preset, it also removes the internal presets
// that exist only beside one, and returns them.
func DisablePreset(scope string, catalog *Catalog, item CatalogItem, force bool, targets ...harness.Registrar) (string, []CatalogItem, error) {
	status, err := disablePreset(scope, item, force, targets...)
	if err != nil || item.Internal {
		return status, nil, err
	}
	removed, err := RemoveAutoManagedFactoryPresets(scope, catalog, false, targets...)
	if err != nil {
		return status, removed, fmt.Errorf("%s is %s, but removing the internal presets beside it failed: %w", item.Name, status, err)
	}
	return status, removed, nil
}

func disablePreset(scope string, item CatalogItem, force bool, targets ...harness.Registrar) (string, error) {
	hookName := strings.TrimSuffix(item.HookFile, ".py")
	if hookName == "" {
		return "", fmt.Errorf("invalid preset hook_file for %s", item.Name)
	}

	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return "", err
	}

	entry, hasEntry := manifest.Hooks[hookName]
	isFactoryMatch := hasEntry && strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name
	if hasEntry {
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

	if _, err := claude.RemoveManagedHook(scope, hookName, disableOptions(item, isFactoryMatch), targets...); err != nil {
		return "", err
	}
	return "disabled", nil
}

// disableOptions archives the guard a preset disable removes, unless it is the
// catalog's own entry, which the catalog can install again. A guard that --force
// removes under the preset's name is archived like any other.
func disableOptions(item CatalogItem, factoryMatch bool) claude.RemoveOptions {
	return claude.RemoveOptions{CatalogCopy: factoryMatch, Reason: "removed by disabling preset " + item.Name}
}

// EnsureAutoManagedFactoryPresets reinstalls internal auto-enabled presets that drifted
// and returns the ones it installed, or would install under dryRun.
func EnsureAutoManagedFactoryPresets(scope string, catalog *Catalog, dryRun bool, targets ...harness.Registrar) ([]CatalogItem, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	if !factoryPresetEnabled(manifest, catalog, true) {
		return nil, nil
	}

	var ensured []CatalogItem
	for _, item := range catalog.InternalAutoEnabled() {
		hookName := strings.TrimSuffix(item.HookFile, ".py")
		if hookName == "" {
			return ensured, fmt.Errorf("invalid preset hook_file for %s", item.Name)
		}

		entry, hasEntry := manifest.Hooks[hookName]
		needsInstall := true
		if hasEntry && strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name {
			var reason string
			needsInstall, reason, err = FactoryPresetNeedsReinstall(scope, catalog, item, hookName, entry, targets...)
			if err != nil {
				return ensured, err
			}
			_ = reason
		}

		if !needsInstall {
			continue
		}

		ensured = append(ensured, item)
		if dryRun {
			continue
		}

		if _, err := InstallFactoryPreset(scope, catalog, item, true, targets...); err != nil {
			return ensured, err
		}

		manifest, err = claude.LoadManifest(scope)
		if err != nil {
			return ensured, err
		}
	}

	return ensured, nil
}

// RemoveAutoManagedFactoryPresets removes the internal auto-managed presets when no
// visible factory preset is enabled, and returns the ones it removed, or would remove
// under dryRun. Each removal syncs the rules, so their Steer Rules lines go too.
func RemoveAutoManagedFactoryPresets(scope string, catalog *Catalog, dryRun bool, targets ...harness.Registrar) ([]CatalogItem, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	if factoryPresetEnabled(manifest, catalog, true) {
		return nil, nil
	}
	return removeAutoManaged(scope, catalog, manifest, dryRun, targets...)
}

func removeAutoManaged(scope string, catalog *Catalog, manifest *claude.Manifest, dryRun bool, targets ...harness.Registrar) ([]CatalogItem, error) {
	var removed []CatalogItem
	for _, item := range catalog.InternalAutoEnabled() {
		hookName := strings.TrimSuffix(item.HookFile, ".py")
		entry, ok := manifest.Hooks[hookName]
		if !ok || !strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) || strings.TrimSpace(entry.SourceID) != item.Name {
			continue
		}
		removed = append(removed, item)
		if dryRun {
			continue
		}
		opts := claude.RemoveOptions{CatalogCopy: true, Reason: "no factory preset remains enabled"}
		if _, err := claude.RemoveManagedHook(scope, hookName, opts, targets...); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// factoryPresetEnabled reports whether the manifest holds a factory guard other than
// an internal preset: a visible preset, or, when orphans count, an orphan whose
// preset left the catalog. Internal auto-managed presets exist only beside one.
func factoryPresetEnabled(manifest *claude.Manifest, catalog *Catalog, orphans bool) bool {
	for _, entry := range manifest.Hooks {
		if !strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) {
			continue
		}
		sourceID := strings.TrimSpace(entry.SourceID)
		item, ok := catalog.Find(sourceID)
		if (ok && !item.Internal) || (!ok && orphans) {
			return true
		}
	}
	return false
}

// SyncFactoryPresets reconciles enabled factory presets with the embedded catalog.
func SyncFactoryPresets(scope string, catalog *Catalog, options SyncOptions, targets ...harness.Registrar) (*SyncResult, error) {
	result := &SyncResult{}

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
	// A pruned orphan is gone by the end of this sync, so it keeps nothing installed.
	visibleEnabled := factoryPresetEnabled(manifest, catalog, !options.PruneOrphans)

	ensuredInternal := map[string]struct{}{}
	for _, hookName := range factoryHookNames {
		entry := manifest.Hooks[hookName]
		sourceID := strings.TrimSpace(entry.SourceID)

		item, found := catalog.Find(sourceID)
		if sourceID == "" || !found {
			result.Orphans = append(result.Orphans, SyncOrphan{
				HookName: hookName,
				SourceID: sourceID,
			})

			if options.PruneOrphans {
				if options.DryRun {
					result.OrphansPruned++
					result.DryRunPlanned++
				} else {
					opts := claude.RemoveOptions{Reason: "its preset is no longer in the catalog"}
					if _, err := claude.RemoveManagedHook(scope, hookName, opts, targets...); err != nil {
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
			if !visibleEnabled {
				continue
			}
			ensuredInternal[item.Name] = struct{}{}
		}

		result.CheckedFactory++
		needsInstall, driftReason, err := FactoryPresetNeedsReinstall(scope, catalog, item, hookName, entry, targets...)
		if err != nil {
			return result, err
		}
		if !needsInstall {
			result.UpToDate++
			continue
		}

		switch driftReason {
		case claude.DriftMetadata:
			result.MetadataDrift++
		case claude.DriftMissingFile:
			result.MissingFile++
		case claude.DriftContent:
			result.ContentDrift++
		}

		if options.DryRun {
			result.Reinstalled++
			result.DryRunPlanned++
			continue
		}
		if _, err := InstallFactoryPreset(scope, catalog, item, true, targets...); err != nil {
			return result, err
		}
		result.Reinstalled++
	}

	if !visibleEnabled {
		removed, err := removeAutoManaged(scope, catalog, manifest, options.DryRun, targets...)
		result.InternalRemoved = len(removed)
		if options.DryRun {
			result.DryRunPlanned += len(removed)
		}
		if err != nil {
			return result, err
		}
	}
	for _, item := range catalog.InternalAutoEnabled() {
		if !visibleEnabled {
			break
		}
		if _, seen := ensuredInternal[item.Name]; seen {
			continue
		}
		result.InternalEnsured++
		if options.DryRun {
			result.Reinstalled++
			result.DryRunPlanned++
			continue
		}
		if _, err := InstallFactoryPreset(scope, catalog, item, true, targets...); err != nil {
			return result, err
		}
		result.Reinstalled++
	}

	if options.Strict && result.OrphansKept > 0 {
		return result, fmt.Errorf("strict mode: found %d orphan factory entries", result.OrphansKept)
	}

	return result, nil
}

// FactoryPresetNeedsReinstall reports whether a factory preset drifted from the catalog.
func FactoryPresetNeedsReinstall(scope string, catalog *Catalog, item CatalogItem, hookName string, entry claude.ManifestEntry, targets ...harness.Registrar) (bool, string, error) {
	desired, err := DesiredHookFromCatalogItem(catalog, item)
	if err != nil {
		return false, "", err
	}

	if hookName != desired.HookName {
		return true, claude.DriftMetadata, nil
	}

	drifted, reason, err := claude.DesiredHookDrift(scope, desired, entry, true, targets...)
	if err != nil {
		return false, "", err
	}
	return drifted, reason, nil
}

// ExpectedFactoryPresetContent returns a preset's hook bytes with the metadata header applied.
func ExpectedFactoryPresetContent(catalog *Catalog, item CatalogItem) ([]byte, error) {
	content, err := catalog.ReadHookContent(item)
	if err != nil {
		return nil, err
	}
	return claude.ApplyMetadataHeader(content, claude.SourceFactory, item.PromptVersion, item.HookVersion), nil
}

// Sha256Hex returns the hex-encoded SHA-256 of content.
func Sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
