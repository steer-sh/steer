package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"
	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
)

func writeTestHook(t *testing.T, path string) []byte {
	t.Helper()
	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("failed to write test hook: %v", err)
	}
	return content
}

func TestInstallManagedHookFirstTime(t *testing.T) {
	withProjectTempDir(t)

	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")

	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}
	if result.Overwritten {
		t.Fatal("expected Overwritten=false for first install")
	}
	if result.HookName != "no-var-hook" {
		t.Fatalf("unexpected hook name: %s", result.HookName)
	}

	// Verify file was written
	if _, err := os.Stat(result.HookPath); err != nil {
		t.Fatalf("hook file not found at %s: %v", result.HookPath, err)
	}

	// Verify manifest entry
	entry, exists, err := GetHookEntry(ProjectScope, "no-var-hook")
	if err != nil {
		t.Fatalf("GetHookEntry error: %v", err)
	}
	if !exists {
		t.Fatal("expected manifest entry to exist")
	}
	if entry.Rule != "never use var" {
		t.Fatalf("unexpected rule: %s", entry.Rule)
	}
}

func TestInstallManagedHookUpsertSameHook(t *testing.T) {
	withProjectTempDir(t)

	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")

	// First install
	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Second install with updated rule — should upsert without error
	updatedContent := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n# updated\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var | use const or let",
		Content:  updatedContent,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if !result.Overwritten {
		t.Fatal("expected Overwritten=true for upsert")
	}
	if result.PreviousRule != "never use var" {
		t.Fatalf("expected previous rule to be preserved, got: %s", result.PreviousRule)
	}

	// Verify updated rule in manifest
	entry, exists, err := GetHookEntry(ProjectScope, "no-var-hook")
	if err != nil {
		t.Fatalf("GetHookEntry error: %v", err)
	}
	if !exists {
		t.Fatal("expected manifest entry to exist after upsert")
	}
	if entry.Rule != "never use var | use const or let" {
		t.Fatalf("rule not updated: %s", entry.Rule)
	}
}

func TestInstallManagedHookFileExistsNoManifest(t *testing.T) {
	withProjectTempDir(t)

	// Pre-create the hook file (simulates agent writing it before register)
	hooksDir := filepath.Join(".claude", "hooks")
	hookPath := filepath.Join(hooksDir, "no-var-hook.py")
	content := writeTestHook(t, hookPath)

	// Register should succeed even though file already exists
	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("register with pre-existing file failed: %v", err)
	}
	if result.Overwritten {
		t.Fatal("expected Overwritten=false (no prior manifest entry)")
	}
}

func TestInstallManagedHookRefusesFileNameOutsideStore(t *testing.T) {
	withProjectTempDir(t)

	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir error: %v", err)
	}

	for _, fileName := range []string{"../escaped.py", "../../escaped.py", "nested/escaped.py", "/tmp/escaped.py"} {
		_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
			HookName: "escape-probe",
			FileName: fileName,
			Rule:     "probe rule | probe",
			Content:  []byte(blockingHookContent),
			Metadata: HookInstallMetadata{Source: SourceGenerated},
		})
		if err == nil {
			t.Fatalf("InstallManagedHook accepted file name %q", fileName)
		}

		escaped := filepath.Clean(filepath.Join(hooksDir, fileName))
		if _, statErr := os.Stat(escaped); statErr == nil {
			t.Fatalf("file name %q wrote %s outside the store", fileName, escaped)
		}

		manifest, err := LoadManifest(ProjectScope)
		if err != nil {
			t.Fatalf("LoadManifest error: %v", err)
		}
		if _, recorded := manifest.Hooks["escape-probe"]; recorded {
			t.Fatalf("file name %q left a manifest entry", fileName)
		}
	}
}

func TestInstallManagedHookRefusesFileNameOwnedByAnotherHook(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "alpha",
		FileName: "shared.py",
		Rule:     "alpha rule | do alpha",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}); err != nil {
		t.Fatalf("InstallManagedHook returned error for alpha: %v", err)
	}

	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "beta",
		FileName: "shared.py",
		Rule:     "beta rule | do beta",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil {
		t.Fatalf("InstallManagedHook let beta take the file alpha owns")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("error does not name the owning hook: %v", err)
	}

	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}
	if _, recorded := manifest.Hooks["beta"]; recorded {
		t.Fatalf("beta kept a manifest entry after the refusal")
	}
	if got := strings.TrimSpace(manifest.Hooks["alpha"].File); got != "shared.py" {
		t.Fatalf("alpha lost its file: %q", got)
	}
}

func TestInstallManagedHookUpdatesItsOwnFile(t *testing.T) {
	withProjectTempDir(t)

	for i := 0; i < 2; i++ {
		if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
			HookName: "alpha",
			FileName: "alpha.py",
			Rule:     "alpha rule | do alpha",
			Content:  []byte(blockingHookContent),
			Metadata: HookInstallMetadata{Source: SourceGenerated},
		}); err != nil {
			t.Fatalf("InstallManagedHook refused its own file on pass %d: %v", i, err)
		}
	}
}

// A guard file the repository carries as a symlink is refused, and the file it
// names is left as it was.
func TestInstallManagedHookRefusesASymlinkedGuardFile(t *testing.T) {
	withProjectTempDir(t)
	if err := EnsureDirectories(ProjectScope); err != nil {
		t.Fatal(err)
	}
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hooksDir, "linked.py")); err != nil {
		t.Fatal(err)
	}

	_, err = InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "linked",
		FileName: "linked.py",
		Rule:     "probe rule | probe",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil {
		t.Fatal("InstallManagedHook wrote through a symlinked guard file")
	}
	if raw, _ := os.ReadFile(outside); string(raw) != "kept\n" {
		t.Fatalf("the outside file changed: %q", raw)
	}
}

// A manifest the repository carries as a dangling symlink is refused, so
// registration creates no file at its target.
func TestInstallManagedHookRefusesASymlinkedManifest(t *testing.T) {
	withProjectTempDir(t)
	if err := EnsureDirectories(ProjectScope); err != nil {
		t.Fatal(err)
	}
	manifestPath, err := GetManifestFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "created.json")
	if err := os.Symlink(outside, manifestPath); err != nil {
		t.Fatal(err)
	}

	_, err = InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "probe",
		Rule:     "probe rule | probe",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil {
		t.Fatal("InstallManagedHook wrote through a symlinked manifest")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("registration created the link's target: %v", err)
	}
}

func TestGuardFilenameReplacementRetiresTheOldImplementation(t *testing.T) {
	withProjectTempDir(t)
	input := InstallManagedHookInput{HookName: "policy", FileName: "policy.py", Rule: "block OLD", Content: []byte(blockingHook("OLD"))}
	if _, err := InstallManagedHook(ProjectScope, input); err != nil {
		t.Fatal(err)
	}
	input.FileName, input.Rule, input.Content = "implementation.py", "block NEW", []byte(blockingHook("NEW"))
	input.Fixtures = &fixture.File{Version: fixture.Version, Hook: "policy", Cases: []fixture.Case{
		bashCase("retired token is allowed", "echo OLD", fixture.ExpectAllow),
		bashCase("replacement token is blocked", "echo NEW", fixture.ExpectBlock),
	}}
	result, err := InstallManagedHook(ProjectScope, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verification == nil || len(result.Verification.Reports) != 1 || !result.Verification.Reports[0].Tested() {
		t.Fatalf("replacement fixtures did not verify the owning guard: %+v", result.Verification)
	}
	dir := filepath.Dir(result.HookPath)
	if _, err := os.Stat(filepath.Join(dir, "policy.py")); !os.IsNotExist(err) {
		t.Fatalf("previous implementation remains in the store: %v", err)
	}
	if entries, err := archive.List(dir); err != nil || len(entries) != 0 {
		t.Fatalf("continuous replacement recorded a removal: %+v, %v", entries, err)
	}
	if agrees, err := MasterEmbedsGuard(ProjectScope, "policy"); err != nil || !agrees {
		t.Fatalf("master lost the registered guard identity: %v, %v", agrees, err)
	}
	identity, err := ReadMasterIdentity(dir)
	if err != nil || identity.Hooks["policy"].RuleHash != HashRule("block NEW") {
		t.Fatalf("master identity cannot resolve the firing's guard id: %+v, %v", identity, err)
	}
	removed, err := RemoveManagedHook(ProjectScope, "implementation", RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if removed.HookName != "policy" || removed.Rule != "block NEW" {
		t.Fatalf("filename removal lost its manifest owner: %+v", removed)
	}
	entry, err := archive.Load(removed.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	var stored ManifestEntry
	if err := json.Unmarshal(entry.Manifest, &stored); err != nil || stored.File != "implementation.py" {
		t.Fatalf("removal did not archive replacement metadata: %+v, %v", stored, err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil || len(manifest.Hooks) != 0 {
		t.Fatalf("removal left a guard registered: %+v, %v", manifest, err)
	}
	if hooks, err := ListIndividualHooks(ProjectScope); err != nil || len(hooks) != 0 {
		t.Fatalf("removal left an implementation active: %+v, %v", hooks, err)
	}
	if _, err := RestoreArchived(ProjectScope, entry); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestGuardFilenameReplacementRetryFinishesAfterManifestWriteFailure(t *testing.T) {
	withProjectTempDir(t)
	input := InstallManagedHookInput{HookName: "policy", FileName: "policy.py", Rule: "block OLD", Content: []byte(blockingHook("OLD"))}
	if _, err := InstallManagedHook(ProjectScope, input); err != nil {
		t.Fatal(err)
	}
	path, err := GetManifestFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(external, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	input.FileName, input.Rule, input.Content = "replacement.py", "block NEW", []byte(blockingHook("NEW"))
	if _, err := InstallManagedHook(ProjectScope, input); err == nil {
		t.Fatal("replacement did not report the refused manifest write")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := InstallManagedHook(ProjectScope, input)
	if err != nil {
		t.Fatalf("replacement retry: %v", err)
	}
	report, err := (fixture.Runner{}).Run(result.HookPath, filepath.Join(filepath.Dir(path), MasterHookName), fixture.File{
		Version: fixture.Version, Hook: "policy", Cases: []fixture.Case{
			bashCase("retired token is allowed", "echo OLD", fixture.ExpectAllow),
			bashCase("replacement token is blocked", "echo NEW", fixture.ExpectBlock),
		},
	})
	if err != nil || !report.Tested() {
		t.Fatalf("retry retained obsolete execution: %+v, %v", report, err)
	}
}

func TestGuardFilenameReplacementRefusesLinkedPreviousImplementation(t *testing.T) {
	withProjectTempDir(t)
	input := InstallManagedHookInput{HookName: "policy", FileName: "policy.py", Rule: "block OLD", Content: []byte(blockingHook("OLD"))}
	installed, err := InstallManagedHook(ProjectScope, input)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.py")
	if err := os.Rename(installed.HookPath, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, installed.HookPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	input.FileName = "replacement.py"
	if _, err := InstallManagedHook(ProjectScope, input); err == nil {
		t.Fatal("replacement accepted a linked previous implementation")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(installed.HookPath), input.FileName)); !os.IsNotExist(err) {
		t.Fatalf("refused replacement wrote another implementation: %v", err)
	}
	entry, found, err := GetHookEntry(ProjectScope, "policy")
	if err != nil || !found || entry.File != "policy.py" {
		t.Fatalf("refusal changed ownership: %+v, %v", entry, err)
	}
}

// A rule with a line break would leave its later lines outside the managed bullet, and
// a "## " line among them would end the managed section early.
func TestInstallersRefuseMultilineRuleText(t *testing.T) {
	withProjectTempDir(t)

	for _, text := range []string{"first\n## Injected\nthird", "first\rsecond"} {
		if _, err := InstallManagedRule(ProjectScope, "multi", text); err == nil {
			t.Errorf("InstallManagedRule accepted %q", text)
		}
		if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
			HookName: "multi-hook",
			Rule:     text,
			Content:  []byte(blockingHookContent),
			Metadata: HookInstallMetadata{Source: SourceGenerated},
		}); err == nil {
			t.Errorf("InstallManagedHook accepted %q", text)
		}
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}
	if len(manifest.Rules) != 0 || len(manifest.Hooks) != 0 {
		t.Fatalf("a refused install left manifest entries: rules=%v hooks=%v", manifest.Rules, manifest.Hooks)
	}
}

// A guard's name lands in the master's Python source and in the shell command its
// block footer prints, so a name outside the plain charset is refused at install.
func TestInstallManagedHookRefusesAnUnsafeGuardName(t *testing.T) {
	withProjectTempDir(t)
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct{ hookName, fileName string }{
		{"ok", "ok\nimport os\n#.py"}, {"ok", "a b.py"}, {"ok", "x$(id).py"},
		{"ok", "-flag.py"}, {"ok", ".hidden.py"}, {"x$(id)", "ok.py"},
	}
	for _, c := range cases {
		fileName := c.fileName
		_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
			HookName: c.hookName,
			FileName: fileName,
			Rule:     "probe rule | probe",
			Content:  []byte(blockingHookContent),
			Metadata: HookInstallMetadata{Source: SourceGenerated},
		})
		if err == nil || !strings.Contains(err.Error(), "a guard name holds only") {
			t.Errorf("InstallManagedHook(%q, %q) = %v; want the guard-name refusal", c.hookName, fileName, err)
		}
		if _, statErr := os.Stat(filepath.Join(hooksDir, fileName)); statErr == nil {
			t.Errorf("guard name %q was written into the store", fileName)
		}
	}
}

// An install into a store that already holds an unsafe name is refused before it
// writes, since the rebuild that follows the write would refuse the store.
func TestInstallManagedHookRefusesAStoreHoldingAnUnsafeName(t *testing.T) {
	withProjectTempDir(t)
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "a b.py"), []byte(blockingHookContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "good",
		FileName: "good.py",
		Rule:     "probe rule | probe",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil || !strings.Contains(err.Error(), "a guard name holds only") {
		t.Fatalf("InstallManagedHook = %v; want the guard-name refusal for the store", err)
	}
	if _, statErr := os.Stat(filepath.Join(hooksDir, "good.py")); statErr == nil {
		t.Error("the refused install wrote good.py")
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Hooks["good"]; ok {
		t.Error("the refused install wrote a manifest entry")
	}
}

// seedSharedGuardFile leaves foo.py on disk owned by the manifest entries given.
func seedSharedGuardFile(t *testing.T, entries map[string]ManifestEntry) string {
	t.Helper()
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "foo.py")
	writeTestHook(t, path)
	if err := SaveManifest(ProjectScope, &Manifest{Hooks: entries}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallManagedHookRefusesAFileAnEntryOwnsByItsKey(t *testing.T) {
	withProjectTempDir(t)
	path := seedSharedGuardFile(t, map[string]ManifestEntry{"foo": {Rule: "r1"}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "bar",
		FileName: "foo.py",
		Rule:     "bar rule",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil || !strings.Contains(err.Error(), `"foo"`) {
		t.Fatalf("install over foo's file: want a refusal naming foo, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the refused install rewrote foo's guard")
	}
}

func TestRemovingOneOwnerKeepsASharedGuardFile(t *testing.T) {
	for _, remove := range []string{"foo", "bar"} {
		t.Run(remove, func(t *testing.T) {
			withProjectTempDir(t)
			path := seedSharedGuardFile(t, map[string]ManifestEntry{
				"foo": {Rule: "r1"},
				"bar": {File: "foo.py", Rule: "r2"},
			})

			if _, err := RemoveManagedHook(ProjectScope, remove, RemoveOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("removing %s took the file the other entry owns: %v", remove, err)
			}
			manifest, err := LoadManifest(ProjectScope)
			if err != nil {
				t.Fatal(err)
			}
			if _, kept := manifest.Hooks[remove]; kept || len(manifest.Hooks) != 1 {
				t.Fatalf("removing %s left entries %v", remove, manifest.Hooks)
			}
		})
	}
}

func TestGuardFilenameReplacementKeepsAFileAnotherEntryOwns(t *testing.T) {
	withProjectTempDir(t)
	path := seedSharedGuardFile(t, map[string]ManifestEntry{
		"foo": {Rule: "r1"},
		"bar": {File: "foo.py", Rule: "r2"},
	})

	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "bar",
		FileName: "bar.py",
		Rule:     "r2",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("moving bar to its own file took the file foo owns: %v", err)
	}
}

func TestRekeyManagedHookMovesTheEntryAndFixtures(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "old",
		FileName: "old.py",
		Rule:     "r1",
		Content:  []byte(blockingHookContent),
		Metadata: HookInstallMetadata{Source: SourceFactory, SourceID: "p"},
		Fixtures: &fixture.File{Version: fixture.Version, Cases: []fixture.Case{
			bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := RekeyManagedHook(ProjectScope, "old", "new", "new.py"); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := manifest.Hooks["old"]; kept || manifest.Hooks["new"].File != "old.py" {
		t.Fatalf("the entry did not move whole: %+v", manifest.Hooks)
	}
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := fixture.Load(dir, "old"); err != nil || ok {
		t.Fatalf("the old fixtures survived: ok=%v err=%v", ok, err)
	}
	moved, ok, err := fixture.Load(dir, "new")
	if err != nil || !ok || moved.Hook != "new" {
		t.Fatalf("the fixtures did not move: %+v ok=%v err=%v", moved, ok, err)
	}
}

func TestRefuseHookRekeyNamesEachConflict(t *testing.T) {
	withProjectTempDir(t)
	if err := SaveManifest(ProjectScope, &Manifest{
		Hooks: map[string]ManifestEntry{
			"old":   {Rule: "r1"},
			"taken": {Rule: "r2"},
		},
		Rules: map[string]RuleEntry{"a-rule": {Text: "r3"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ from, to, file string }{
		{"absent", "new", "new.py"},
		{"old", "taken", "new.py"},
		{"old", "a-rule", "new.py"},
		{"old", "new", "taken.py"},
	} {
		if err := RefuseHookRekey(ProjectScope, tc.from, tc.to, tc.file); err == nil {
			t.Errorf("%+v: no refusal", tc)
		}
	}
	if err := RefuseHookRekey(ProjectScope, "old", "new", "old.py"); err != nil {
		t.Errorf("a rename keeping its own file was refused: %v", err)
	}
}

func TestRekeyManagedHookNamesTheFileAnEmptyEntryOwned(t *testing.T) {
	withProjectTempDir(t)
	if err := SaveManifest(ProjectScope, &Manifest{Hooks: map[string]ManifestEntry{"old": {Rule: "r1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := RekeyManagedHook(ProjectScope, "old", "new", "new.py"); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Hooks["new"].File; got != "old.py" {
		t.Fatalf("the moved entry names %q, not the file it owned", got)
	}
}

func TestRemovingAKeyLeavesTheGuardThatOwnsItsStem(t *testing.T) {
	withProjectTempDir(t)
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	writeTestHook(t, filepath.Join(dir, "old.py"))
	writeTestHook(t, filepath.Join(dir, "shared.py"))
	if err := SaveManifest(ProjectScope, &Manifest{Hooks: map[string]ManifestEntry{
		"old": {File: "shared.py", Rule: "r1"},
		"zed": {File: "old.py", Rule: "r2"},
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveManagedHook(ProjectScope, "old", RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := manifest.Hooks["old"]; kept {
		t.Fatal("the named entry survived its removal")
	}
	if _, kept := manifest.Hooks["zed"]; !kept {
		t.Fatal("removing old took the guard that owns old.py")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.py")); err != nil {
		t.Fatalf("removing old took zed's file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared.py")); !os.IsNotExist(err) {
		t.Fatalf("old's own file survived: %v", err)
	}
}
