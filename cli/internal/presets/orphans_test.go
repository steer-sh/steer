package presets

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/claude"
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

func addOrphanFactoryHook(t *testing.T, scope, hookName, sourceID, rule string) {
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

func archivedIDs(t *testing.T) []string {
	t.Helper()
	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestDisableOrphanArchivesTheNamedGuardAlone(t *testing.T) {
	withPresetProjectTempDir(t)
	addOrphanFactoryHook(t, claude.ProjectScope, "old-guard", "old-preset", "old rule")
	if _, err := claude.InstallManagedHook(claude.ProjectScope, claude.InstallManagedHookInput{
		HookName: "mine", Rule: "my rule", Content: []byte("def main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceGenerated, SourceID: "old-preset-twin"},
	}); err != nil {
		t.Fatal(err)
	}

	if found, err := DisableOrphan(claude.ProjectScope, "mine"); err != nil || found {
		t.Fatalf("a generated guard read as an orphan (found=%v err=%v)", found, err)
	}
	found, err := DisableOrphan(claude.ProjectScope, "old-preset")
	if err != nil || !found {
		t.Fatalf("the orphan named by its preset was not found (err=%v)", err)
	}
	if manifestHas(t, "old-guard") || !manifestHas(t, "mine") {
		t.Fatal("want the orphan gone and the generated guard kept")
	}
	if got := archivedIDs(t); strings.Join(got, ",") != "old-guard" {
		t.Fatalf("the removed orphan has no archive entry: %v", got)
	}

	// A name that is one orphan's id and another's preset is refused, not guessed.
	addOrphanFactoryHook(t, claude.ProjectScope, "shared", "first-preset", "first rule")
	addOrphanFactoryHook(t, claude.ProjectScope, "second-guard", "shared", "second rule")
	if _, err := DisableOrphan(claude.ProjectScope, "shared"); err == nil {
		t.Fatal("an ambiguous name removed a guard")
	}
	if !manifestHas(t, "shared") || !manifestHas(t, "second-guard") {
		t.Fatal("an ambiguous name removed a guard")
	}
}

// removedPresets are the optional presets Steer no longer ships. An install from
// an earlier Steer still holds their guards, and may hold the self-protection
// guard it installed beside them.
var removedPresets = []string{
	"api-key-detection", "dangerous-python-imports", "data-exfiltration-prevention",
	"env-dump-prevention", "environment-variable-enumeration-prevention",
	"eval-usage-security", "hardcoded-secret-reference-prevention",
	"package-json-git-deps", "postinstall-script-prevention",
	"python-command-injection", "recursive-search-prevention", "sql-injection-prevention",
	"steer-self-protection",
}

// A guard installed from a preset keeps running as an orphan until a prune
// archives and removes it; a dry run names them all and changes nothing.
func TestARemovedPresetKeepsRunningAsAnOrphanUntilPruned(t *testing.T) {
	withPresetProjectTempDir(t)
	for _, name := range removedPresets {
		addOrphanFactoryHook(t, claude.ProjectScope, name, name, name+" rule")
	}

	planned, err := PruneOrphans(claude.ProjectScope, true)
	if err != nil || len(planned) != len(removedPresets) {
		t.Fatalf("a dry run named %d orphans (err=%v), want %d", len(planned), err, len(removedPresets))
	}
	for _, name := range removedPresets {
		if !manifestHas(t, name) {
			t.Fatalf("a dry run removed %s", name)
		}
	}

	pruned, err := PruneOrphans(claude.ProjectScope, false)
	if err != nil || len(pruned) != len(removedPresets) {
		t.Fatalf("pruned %d orphans (err=%v), want %d", len(pruned), err, len(removedPresets))
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Hooks) != 0 {
		t.Fatalf("guards left after prune: %v", manifest.Hooks)
	}
	want := append([]string(nil), removedPresets...)
	sort.Strings(want)
	if got := archivedIDs(t); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("archived %v, want every pruned guard", got)
	}
}
