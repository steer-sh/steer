package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
)

func TestRemovingAGuardArchivesItsFileFixturesAndEntry(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	fixtures, err := os.ReadFile(fixture.Path(dir, "alpha"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{Reason: "blocks too much"})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := archive.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Dir != result.ArchivePath {
		t.Fatalf("archive holds %d entries; the result names %q", len(entries), result.ArchivePath)
	}
	e := entries[0]
	if e.Tier != "guard" || e.ID != "alpha" || e.Scope != ProjectScope || e.Reason != "blocks too much" || len(e.Missing) != 0 {
		t.Fatalf("entry = %+v", e.Entry)
	}
	var entry ManifestEntry
	if err := json.Unmarshal(e.Manifest, &entry); err != nil || entry.Rule != "block alpha" {
		t.Fatalf("archived manifest entry = %+v, %v", entry, err)
	}
	guard, err := os.ReadFile(filepath.Join(e.Dir, "alpha.py"))
	if err != nil || string(guard) != blockingHook("ALPHA") {
		t.Fatalf("archived guard = %q, %v", guard, err)
	}
	stored, err := os.ReadFile(filepath.Join(e.Dir, ArchivedFixturesName))
	if err != nil || string(stored) != string(fixtures) {
		t.Fatalf("archived fixtures = %q, %v", stored, err)
	}

	hooks, err := ListIndividualHooks(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 0 {
		t.Fatalf("the archived guard is still listed as installed: %v", hooks)
	}
	master, err := os.ReadFile(filepath.Join(dir, MasterHookName))
	if err == nil && strings.Contains(string(master), "ALPHA") {
		t.Fatal("the archived guard reached the master")
	}
}

// onlyEntry reads the one archive entry a removal left in the project store.
func onlyEntry(t *testing.T) archive.Stored {
	t.Helper()
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive holds %d entries, want 1", len(entries))
	}
	return entries[0]
}

func TestRemovingARuleArchivesItsEntry(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallManagedRule(ProjectScope, "config-path", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}

	result, err := RemoveManagedRule(ProjectScope, "config-path", "agents follow it without being told")
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)
	if e.Dir != result.ArchivePath || e.Tier != "instruction" || e.ID != "config-path" || e.Reason != "agents follow it without being told" {
		t.Fatalf("entry = %+v; result names %q", e.Entry, result.ArchivePath)
	}
	var entry RuleEntry
	if err := json.Unmarshal(e.Manifest, &entry); err != nil || entry.Text != "resolve a config path from the repo root" {
		t.Fatalf("archived rule = %+v, %v", entry, err)
	}
}

func TestRemovingAnAddendumEntryArchivesIt(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallAddendum(ProjectScope, "no-style", "style is not a finding"); err != nil {
		t.Fatal(err)
	}

	result, err := RemoveAddendum(ProjectScope, "no-style", "hid a real defect")
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)
	if e.Dir != result.ArchivePath || e.Tier != "review" || e.ID != "no-style" || e.Reason != "hid a real defect" {
		t.Fatalf("entry = %+v; result names %q", e.Entry, result.ArchivePath)
	}
	var entry AddendumEntry
	if err := json.Unmarshal(e.Manifest, &entry); err != nil || entry.Text != "style is not a finding" {
		t.Fatalf("archived addendum entry = %+v, %v", entry, err)
	}
}

func TestAFailedArchiveWriteKeepsTheRule(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	withProjectTempDir(t)
	if _, err := InstallManagedRule(ProjectScope, "config-path", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, archive.DirName)
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if _, err := RemoveManagedRule(ProjectScope, "config-path", ""); err == nil {
		t.Fatal("the removal went ahead without an archive")
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Rules["config-path"]; !ok {
		t.Fatal("the rule was deleted")
	}
}

func TestACatalogCopyIsRemovedWithoutAnArchive(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})

	result, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{CatalogCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ArchivePath != "" {
		t.Fatalf("a catalog copy was archived at %s", result.ArchivePath)
	}
	if _, err := os.Stat(filepath.Join(dir, archive.DirName)); !os.IsNotExist(err) {
		t.Fatal("a catalog copy created an archive")
	}
}

func TestAFailedArchiveWriteKeepsTheGuard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	locked := filepath.Join(dir, archive.DirName)
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if _, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{}); err == nil {
		t.Fatal("the removal went ahead without an archive")
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.py")); err != nil {
		t.Fatalf("the guard file was deleted: %v", err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Hooks["alpha"]; !ok {
		t.Fatal("the manifest entry was deleted")
	}
}
