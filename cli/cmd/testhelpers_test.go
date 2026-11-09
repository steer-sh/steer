package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"

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

// loadCatalogAndOneVisiblePreset returns the embedded catalog with two visible
// presets added, and the first of them. The embedded catalog holds none.
func loadCatalogAndOneVisiblePreset(t *testing.T) (*presets.Catalog, presets.CatalogItem) {
	t.Helper()

	embedded, err := presets.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}
	assets := fstest.MapFS{}
	for _, item := range embedded.Presets {
		content, err := embedded.ReadHookContent(item)
		if err != nil {
			t.Fatal(err)
		}
		assets["hooks/"+item.HookFile] = &fstest.MapFile{Data: content}
	}
	items := append([]presets.CatalogItem(nil), embedded.Presets...)
	for _, name := range []string{"test-guard-a", "test-guard-b"} {
		items = append(items, presets.CatalogItem{
			Name: name, Description: name + " description", Rule: name + " rule",
			HookFile: name + ".py", Bundles: []string{presets.BundleSecurity}, OS: []string{"all"},
			PromptVersion: "test-prompt-v1", HookVersion: "test-hook-v1",
		})
		assets["hooks/"+name+".py"] = &fstest.MapFile{Data: []byte("#!/usr/bin/env python3\n# " + name + "\n\ndef main():\n    return\n")}
	}
	catalog := presets.NewCatalog(embedded.CatalogVersion, items, assets)

	names, err := catalog.BundleMembers(presets.BundleSecurity)
	if err != nil {
		t.Fatalf("BundleMembers returned error: %v", err)
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

	if _, err := presets.EnablePreset(scope, catalog, visible, false); err != nil {
		t.Fatalf("EnablePreset returned error: %v", err)
	}
	if _, err := presets.EnsureAutoManagedFactoryPresets(scope, catalog, false); err != nil {
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
