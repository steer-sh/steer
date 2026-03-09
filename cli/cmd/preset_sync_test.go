package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/presets"
)

func withCmdProjectTempDir(t *testing.T) string {
	t.Helper()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	return tempDir
}

func loadCatalogAndOneVisiblePreset(t *testing.T) (*presets.Catalog, presets.CatalogItem) {
	t.Helper()

	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	names, err := catalog.BundleMembers(presets.BundleSecurity)
	if err != nil {
		t.Fatalf("BundleMembers returned error: %v", err)
	}
	if len(names) == 0 {
		t.Fatalf("expected at least one security preset")
	}
	sort.Strings(names)

	item, ok := catalog.Find(names[0])
	if !ok {
		t.Fatalf("expected visible preset %s in catalog", names[0])
	}
	return catalog, item
}

func installVisibleAndInternal(t *testing.T, scope string, catalog *presets.Catalog, visible presets.CatalogItem) {
	t.Helper()

	if _, err := enablePreset(scope, catalog, visible, false); err != nil {
		t.Fatalf("enablePreset returned error: %v", err)
	}
	if _, err := ensureAutoManagedFactoryPresets(scope, catalog, false); err != nil {
		t.Fatalf("ensureAutoManagedFactoryPresets returned error: %v", err)
	}
}

func addOrphanFactoryHook(t *testing.T, scope, hookName, sourceID, rule string) string {
	t.Helper()

	_, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: hookName,
		FileName: hookName + ".py",
		Rule:     rule,
		Content:  []byte("#!/usr/bin/env python3\n\ndef main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{
			Source:        claude.SourceFactory,
			SourceID:      sourceID,
			SourceVersion: "v0-orphan",
			PromptVersion: "orphan-prompt",
			HookVersion:   "orphan-hook",
		},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook orphan returned error: %v", err)
	}

	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	return filepath.Join(hooksDir, hookName+".py")
}

func TestPresetSyncNoOpWhenFullyMatching(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)

	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.CheckedFactory != 2 {
		t.Fatalf("expected 2 checked presets (visible + internal), got %d", result.CheckedFactory)
	}
	if result.UpToDate != 2 {
		t.Fatalf("expected 2 up-to-date presets, got %d", result.UpToDate)
	}
	if result.Reinstalled != 0 {
		t.Fatalf("expected no reinstalls, got %d", result.Reinstalled)
	}
	if result.OrphansKept != 0 || len(result.Orphans) != 0 {
		t.Fatalf("expected no orphans, got kept=%d entries=%d", result.OrphansKept, len(result.Orphans))
	}
}

func TestPresetSyncReinstallsOnMetadataMismatch(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	hookName := strings.TrimSuffix(visible.HookFile, ".py")
	entry := manifest.Hooks[hookName]
	entry.HookVersion = "tampered-hook-version"
	manifest.Hooks[hookName] = entry
	if err := claude.SaveManifest(claude.ProjectScope, manifest); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
	}

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.MetadataDrift < 1 {
		t.Fatalf("expected metadata drift count >= 1, got %d", result.MetadataDrift)
	}
	if result.Reinstalled < 1 {
		t.Fatalf("expected at least one reinstall, got %d", result.Reinstalled)
	}

	manifest, err = claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	entry = manifest.Hooks[hookName]
	if entry.HookVersion != visible.HookVersion {
		t.Fatalf("expected hook version %q after sync, got %q", visible.HookVersion, entry.HookVersion)
	}
}

func TestPresetSyncReinstallsOnContentTamper(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)

	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	path := filepath.Join(hooksDir, visible.HookFile)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n# tampered\n"), 0o755); err != nil {
		t.Fatalf("failed to tamper hook file: %v", err)
	}

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.ContentDrift < 1 {
		t.Fatalf("expected content drift count >= 1, got %d", result.ContentDrift)
	}

	expected, err := expectedFactoryPresetContent(catalog, visible)
	if err != nil {
		t.Fatalf("expectedFactoryPresetContent returned error: %v", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read synced hook file: %v", err)
	}
	if sha256Hex(expected) != sha256Hex(actual) {
		t.Fatalf("expected hook content to be restored after sync")
	}
}

func TestPresetSyncReinstallsOnMissingFile(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)

	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	path := filepath.Join(hooksDir, visible.HookFile)
	if err := os.Remove(path); err != nil {
		t.Fatalf("failed to remove hook file: %v", err)
	}

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.MissingFile < 1 {
		t.Fatalf("expected missing file count >= 1, got %d", result.MissingFile)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected hook file to be restored, stat err=%v", err)
	}
}

func TestPresetSyncKeepsOrphanByDefault(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-kept", "unknown-catalog-id", "orphan keep rule")

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.OrphansKept != 1 {
		t.Fatalf("expected 1 orphan kept, got %d", result.OrphansKept)
	}
	if len(result.Orphans) != 1 {
		t.Fatalf("expected 1 orphan entry, got %d", len(result.Orphans))
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("expected orphan file to remain, stat err=%v", err)
	}
}

func TestPresetSyncStrictFailsOnOrphan(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	_ = addOrphanFactoryHook(t, claude.ProjectScope, "orphan-strict", "missing-source-id", "orphan strict rule")

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{Strict: true})
	if err == nil {
		t.Fatalf("expected strict mode error on orphan, got nil")
	}
	if result == nil || result.OrphansKept != 1 {
		t.Fatalf("expected strict result with 1 kept orphan, got %+v", result)
	}
}

func TestPresetSyncDryRunPruneStrictDoesNotFailOnOrphan(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-dry-prune-strict", "missing-source-id", "orphan dry prune strict rule")

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{
		DryRun:       true,
		Strict:       true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("expected no strict error when prune-orphans is requested in dry-run, got: %v", err)
	}
	if result.OrphansPruned != 1 {
		t.Fatalf("expected 1 orphan planned for prune, got %d", result.OrphansPruned)
	}
	if result.OrphansKept != 0 {
		t.Fatalf("expected 0 kept orphans in dry-run prune mode, got %d", result.OrphansKept)
	}
	if result.DryRunPlanned < 1 {
		t.Fatalf("expected dry-run planned mutations >= 1, got %d", result.DryRunPlanned)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("expected orphan file to remain during dry-run, stat err=%v", err)
	}
}

func TestPresetSyncPruneOrphansUsesManagedRemovePath(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	rule := "orphan prune rule"
	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-prune", "gone-from-catalog", rule)

	claudeMDPath, err := claude.GetClaudeMDFile(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetClaudeMDFile returned error: %v", err)
	}
	before, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("failed to read CLAUDE.md before sync: %v", err)
	}
	if !strings.Contains(string(before), rule) {
		t.Fatalf("expected orphan rule in CLAUDE.md before prune")
	}

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{PruneOrphans: true})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.OrphansPruned != 1 {
		t.Fatalf("expected 1 pruned orphan, got %d", result.OrphansPruned)
	}

	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan file removed, stat err=%v", err)
	}

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if _, ok := manifest.Hooks["orphan-prune"]; ok {
		t.Fatalf("expected orphan entry removed from manifest")
	}

	after, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("failed to read CLAUDE.md after sync: %v", err)
	}
	if strings.Contains(string(after), rule) {
		t.Fatalf("expected orphan rule removed from CLAUDE.md")
	}
}

func TestPresetSyncDryRunDoesNotMutate(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)

	if _, err := enablePreset(claude.ProjectScope, catalog, visible, false); err != nil {
		t.Fatalf("enablePreset returned error: %v", err)
	}

	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	visiblePath := filepath.Join(hooksDir, visible.HookFile)
	tampered := "#!/usr/bin/env python3\n\ndef main():\n    return\n\nif __name__ == \"__main__\":\n    main()\n"
	if err := os.WriteFile(visiblePath, []byte(tampered), 0o755); err != nil {
		t.Fatalf("failed to tamper hook file: %v", err)
	}

	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-dry-run", "not-in-catalog", "orphan dry run rule")

	result, err := syncFactoryPresets(claude.ProjectScope, catalog, presetSyncOptions{
		DryRun:       true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("syncFactoryPresets returned error: %v", err)
	}
	if result.DryRunPlanned < 2 {
		t.Fatalf("expected at least 2 planned dry-run mutations, got %d", result.DryRunPlanned)
	}

	actual, err := os.ReadFile(visiblePath)
	if err != nil {
		t.Fatalf("failed reading visible hook after dry-run: %v", err)
	}
	expected, err := expectedFactoryPresetContent(catalog, visible)
	if err != nil {
		t.Fatalf("expectedFactoryPresetContent returned error: %v", err)
	}
	if sha256Hex(actual) == sha256Hex(expected) {
		t.Fatalf("expected visible hook tamper to remain during dry-run")
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("expected orphan file to remain in dry-run, stat err=%v", err)
	}

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if _, ok := manifest.Hooks["orphan-dry-run"]; !ok {
		t.Fatalf("expected orphan manifest entry to remain in dry-run")
	}

	internal, ok := catalog.Find("steer-self-protection")
	if !ok {
		t.Fatalf("expected internal self-protection preset")
	}
	internalPath := filepath.Join(hooksDir, internal.HookFile)
	if _, err := os.Stat(internalPath); !os.IsNotExist(err) {
		t.Fatalf("expected internal auto-managed preset not to be installed during dry-run")
	}
}
