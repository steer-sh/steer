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
