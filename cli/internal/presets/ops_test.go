package presets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
)

func withPresetProjectTempDir(t *testing.T) string {
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

func loadCatalogAndOneVisiblePreset(t *testing.T) (*Catalog, CatalogItem) {
	t.Helper()

	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	names, err := catalog.BundleMembers(BundleSecurity)
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

func installVisibleAndInternal(t *testing.T, scope string, catalog *Catalog, visible CatalogItem, targets ...harness.Registrar) {
	t.Helper()

	if _, err := EnablePreset(scope, catalog, visible, false, targets...); err != nil {
		t.Fatalf("EnablePreset returned error: %v", err)
	}
	if _, err := EnsureAutoManagedFactoryPresets(scope, catalog, false, targets...); err != nil {
		t.Fatalf("EnsureAutoManagedFactoryPresets returned error: %v", err)
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
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)

	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
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
	withPresetProjectTempDir(t)
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

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
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
	withPresetProjectTempDir(t)
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

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
	}
	if result.ContentDrift < 1 {
		t.Fatalf("expected content drift count >= 1, got %d", result.ContentDrift)
	}

	expected, err := ExpectedFactoryPresetContent(catalog, visible)
	if err != nil {
		t.Fatalf("ExpectedFactoryPresetContent returned error: %v", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read synced hook file: %v", err)
	}
	if Sha256Hex(expected) != Sha256Hex(actual) {
		t.Fatalf("expected hook content to be restored after sync")
	}
}

func TestPresetSyncReinstallsOnMissingFile(t *testing.T) {
	withPresetProjectTempDir(t)
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

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
	}
	if result.MissingFile < 1 {
		t.Fatalf("expected missing file count >= 1, got %d", result.MissingFile)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected hook file to be restored, stat err=%v", err)
	}
}

func TestPresetSyncKeepsOrphanByDefault(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-kept", "unknown-catalog-id", "orphan keep rule")

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
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
	withPresetProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	_ = addOrphanFactoryHook(t, claude.ProjectScope, "orphan-strict", "missing-source-id", "orphan strict rule")

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{Strict: true})
	if err == nil {
		t.Fatalf("expected strict mode error on orphan, got nil")
	}
	if result == nil || result.OrphansKept != 1 {
		t.Fatalf("expected strict result with 1 kept orphan, got %+v", result)
	}
}

func TestPresetSyncDryRunPruneStrictDoesNotFailOnOrphan(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)

	orphanPath := addOrphanFactoryHook(t, claude.ProjectScope, "orphan-dry-prune-strict", "missing-source-id", "orphan dry prune strict rule")

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{
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
	withPresetProjectTempDir(t)
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

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{PruneOrphans: true})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
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
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)

	if _, err := EnablePreset(claude.ProjectScope, catalog, visible, false); err != nil {
		t.Fatalf("EnablePreset returned error: %v", err)
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

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{
		DryRun:       true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
	}
	if result.DryRunPlanned < 2 {
		t.Fatalf("expected at least 2 planned dry-run mutations, got %d", result.DryRunPlanned)
	}

	actual, err := os.ReadFile(visiblePath)
	if err != nil {
		t.Fatalf("failed reading visible hook after dry-run: %v", err)
	}
	expected, err := ExpectedFactoryPresetContent(catalog, visible)
	if err != nil {
		t.Fatalf("ExpectedFactoryPresetContent returned error: %v", err)
	}
	if Sha256Hex(actual) == Sha256Hex(expected) {
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

func TestDisablingAFactoryPresetWritesNoArchive(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, item := loadCatalogAndOneVisiblePreset(t)
	if _, err := EnablePreset(claude.ProjectScope, catalog, item, false); err != nil {
		t.Fatal(err)
	}

	if _, _, err := DisablePreset(claude.ProjectScope, catalog, item, false); err != nil {
		t.Fatal(err)
	}
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("disabling a catalog preset archived %s", entries[0].ID)
	}
}

func TestPruningAnOrphanArchivesIt(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, _ := loadCatalogAndOneVisiblePreset(t)
	addOrphanFactoryHook(t, claude.ProjectScope, "orphan-prune", "gone-from-catalog", "orphan prune rule")

	if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{PruneOrphans: true}); err != nil {
		t.Fatal(err)
	}
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "orphan-prune" {
		t.Fatalf("the pruned orphan has no archive entry: %+v", entries)
	}
}

func selfProtectionHook(t *testing.T, catalog *Catalog) string {
	t.Helper()
	item, ok := catalog.Find("steer-self-protection")
	if !ok {
		t.Fatal("expected internal preset steer-self-protection")
	}
	return strings.TrimSuffix(item.HookFile, ".py")
}

func manifestHas(t *testing.T, hookName string) bool {
	t.Helper()
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := manifest.Hooks[hookName]
	return ok
}

func TestDisablingTheLastFactoryPresetRemovesSelfProtectionAndItsRules(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible, claude.Registrar{})
	if data, _ := os.ReadFile("CLAUDE.md"); !strings.Contains(string(data), "## Steer Rules") {
		t.Fatalf("setup wrote no rules section:\n%s", data)
	}

	_, removed, err := DisablePreset(claude.ProjectScope, catalog, visible, false, claude.Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "steer-self-protection" {
		t.Fatalf("removed = %+v, want steer-self-protection", removed)
	}
	if manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatal("self-protection stayed after the last factory preset was disabled")
	}
	if data, _ := os.ReadFile("CLAUDE.md"); strings.Contains(string(data), "## Steer Rules") {
		t.Fatalf("the rules section stayed with no rule left:\n%s", data)
	}
}

func TestDisablingOneOfTwoFactoryPresetsKeepsSelfProtection(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	names, err := catalog.BundleMembers(BundleSecurity)
	if err != nil || len(names) < 2 {
		t.Fatalf("need two security presets, got %v (%v)", names, err)
	}
	sort.Strings(names)
	second, _ := catalog.Find(names[1])
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	if _, err := EnablePreset(claude.ProjectScope, catalog, second, false); err != nil {
		t.Fatal(err)
	}

	_, removed, err := DisablePreset(claude.ProjectScope, catalog, visible, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 || !manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatalf("self-protection went while %s stayed enabled (removed %+v)", second.Name, removed)
	}
}

func TestPresetSyncInstallsNoSelfProtectionWithoutAFactoryPreset(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.InternalEnsured != 0 || manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatalf("sync installed self-protection with no factory preset: %+v", result)
	}

	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	hookName := strings.TrimSuffix(visible.HookFile, ".py")
	if _, err := claude.RemoveManagedHook(claude.ProjectScope, hookName, claude.RemoveOptions{CatalogCopy: true}); err != nil {
		t.Fatal(err)
	}
	result, err = SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.InternalRemoved != 1 || manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatalf("sync kept self-protection with no factory preset: %+v", result)
	}
}

func TestPresetSyncKeepsSelfProtectionBesideAnOrphan(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	hookName := strings.TrimSuffix(visible.HookFile, ".py")
	if _, err := claude.RemoveManagedHook(claude.ProjectScope, hookName, claude.RemoveOptions{CatalogCopy: true}); err != nil {
		t.Fatal(err)
	}
	addOrphanFactoryHook(t, claude.ProjectScope, "orphan-guard", "left-the-catalog", "orphan guard rule")

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.InternalRemoved != 0 || !manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatalf("sync removed self-protection while an orphan factory guard stayed: %+v", result)
	}
}

func TestPresetSyncPruningTheLastOrphanRemovesSelfProtection(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	hookName := strings.TrimSuffix(visible.HookFile, ".py")
	if _, err := claude.RemoveManagedHook(claude.ProjectScope, hookName, claude.RemoveOptions{CatalogCopy: true}); err != nil {
		t.Fatal(err)
	}
	addOrphanFactoryHook(t, claude.ProjectScope, "orphan-last", "left-the-catalog", "orphan last rule")

	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{PruneOrphans: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphansPruned != 1 || result.InternalRemoved != 1 || manifestHas(t, selfProtectionHook(t, catalog)) {
		t.Fatalf("one sync must prune the orphan and remove self-protection: %+v", result)
	}
}

// seedRenamedFactoryKey moves an installed preset's manifest entry to an id the
// catalog no longer derives, naming file as the entry's guard file.
func seedRenamedFactoryKey(t *testing.T, visible CatalogItem, oldName, file string) {
	t.Helper()
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	current := strings.TrimSuffix(visible.HookFile, ".py")
	entry := manifest.Hooks[current]
	delete(manifest.Hooks, current)
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if file != visible.HookFile {
		if err := os.Rename(filepath.Join(hooksDir, visible.HookFile), filepath.Join(hooksDir, file)); err != nil {
			t.Fatal(err)
		}
	}
	entry.File = file
	manifest.Hooks[oldName] = entry
	if err := claude.SaveManifest(claude.ProjectScope, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestPresetSyncRepairsARename(t *testing.T) {
	for _, keepsFile := range []bool{true, false} {
		name := "changes the file"
		if keepsFile {
			name = "keeps the file"
		}
		t.Run(name, func(t *testing.T) {
			withPresetProjectTempDir(t)
			catalog, visible := loadCatalogAndOneVisiblePreset(t)
			installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
			file := "old-name.py"
			if keepsFile {
				file = visible.HookFile
			}
			seedRenamedFactoryKey(t, visible, "old-name", file)

			dry, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{DryRun: true})
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if !manifestHas(t, "old-name") {
				t.Fatal("the dry run changed the manifest")
			}

			real, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
			if err != nil {
				t.Fatalf("sync stopped at the rename: %v", err)
			}
			if dry.Reinstalled != real.Reinstalled || dry.MetadataDrift != real.MetadataDrift || dry.DryRunPlanned != real.Reinstalled {
				t.Fatalf("dry run %+v disagrees with the sync %+v", dry, real)
			}
			if manifestHas(t, "old-name") || !manifestHas(t, strings.TrimSuffix(visible.HookFile, ".py")) {
				t.Fatal("the entry did not move to the catalog's id")
			}

			hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(hooksDir, visible.HookFile)); err != nil {
				t.Fatalf("the catalog's file is gone: %v", err)
			}
			if !keepsFile {
				if _, err := os.Stat(filepath.Join(hooksDir, "old-name.py")); !os.IsNotExist(err) {
					t.Fatalf("the old file survived the rename: %v", err)
				}
			}

			again, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if again.Reinstalled != 0 {
				t.Fatalf("a second sync still reinstalls: %+v", again)
			}
		})
	}
}

func TestPresetSyncRenameFailureKeepsTheGuard(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	seedRenamedFactoryKey(t, visible, "old-name", "old-name.py")
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	// The store's writer refuses a symlink, which fails the reinstall.
	blocked := filepath.Join(hooksDir, visible.HookFile)
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.py"), blocked); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{}); err == nil {
		t.Fatal("the sync reported success through a failed reinstall")
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "old-name.py")); err != nil {
		t.Fatalf("the failed repair lost the guard file: %v", err)
	}
	if len(manifest.Hooks) == 0 || manifest.Hooks[strings.TrimSuffix(visible.HookFile, ".py")].File != "old-name.py" {
		t.Fatalf("no entry names the guard after the failed repair: %+v", manifest.Hooks)
	}

	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{}); err != nil {
		t.Fatalf("the retry did not finish the repair: %v", err)
	}
	if _, err := os.Stat(blocked); err != nil {
		t.Fatalf("the retry did not install the catalog's file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "old-name.py")); !os.IsNotExist(err) {
		t.Fatalf("the retry left the old file: %v", err)
	}
}

func TestPresetSyncRenameRefusesAnIDAnotherGuardHolds(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	seedRenamedFactoryKey(t, visible, "old-name", "old-name.py")
	current := strings.TrimSuffix(visible.HookFile, ".py")
	if _, err := claude.InstallManagedHook(claude.ProjectScope, claude.InstallManagedHookInput{
		HookName: current,
		FileName: visible.HookFile,
		Rule:     "the operator's own rule",
		Content:  []byte("#!/usr/bin/env python3\n\ndef main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}); err != nil {
		t.Fatal(err)
	}

	for _, dryRun := range []bool{true, false} {
		if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{DryRun: dryRun}); err == nil {
			t.Fatalf("dryRun=%v: the rename took an id the operator's guard holds", dryRun)
		}
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Hooks[current].Source != claude.SourceManual || !manifestHas(t, "old-name") {
		t.Fatalf("the refusal changed the store: %+v", manifest.Hooks)
	}
}

func TestPresetSyncRemovesADuplicateKeyBeforeRepairingTheCatalogEntry(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	current := strings.TrimSuffix(visible.HookFile, ".py")
	// The stale key sorts after the catalog's id and shares its file, and the
	// catalog's entry has drifted, so repairing that entry first is refused.
	entry := manifest.Hooks[current]
	entry.HookVersion = "drifted"
	manifest.Hooks[current] = entry
	manifest.Hooks["zz-old-name"] = entry
	if err := claude.SaveManifest(claude.ProjectScope, manifest); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if manifestHas(t, "zz-old-name") {
		t.Fatal("the duplicate key survived the sync")
	}
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, visible.HookFile)); err != nil {
		t.Fatalf("removing the duplicate took the preset's file: %v", err)
	}
	entries, err := archive.List(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "zz-old-name" {
		t.Fatalf("the removed duplicate has no archive entry: %+v", entries)
	}
}

func TestPresetSyncFinishesARenameWhoseReinstallFailedOnAnUnchangedFile(t *testing.T) {
	withPresetProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	seedRenamedFactoryKey(t, visible, "old-name", visible.HookFile)
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	// A symlink to the catalog's own bytes reads as current and refuses the write.
	path := filepath.Join(hooksDir, visible.HookFile)
	aside := filepath.Join(t.TempDir(), "aside.py")
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{}); err == nil {
		t.Fatal("the sync reported success through a failed reinstall")
	}

	content, err := os.ReadFile(aside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := SyncFactoryPresets(claude.ProjectScope, catalog, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reinstalled == 0 {
		t.Fatalf("the moved entry read as up to date, so the master keeps the old id: %+v", result)
	}
}
