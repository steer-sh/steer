package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
)

// removeAlpha installs the alpha guard with one fixture, removes it, and
// returns the store directory and the archive entry the removal wrote.
func removeAlpha(t *testing.T) (string, archive.Stored) {
	t.Helper()
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{Reason: "blocks too much"}); err != nil {
		t.Fatal(err)
	}
	return dir, onlyEntry(t)
}

func TestRestoreReinstallsAnArchivedGuardAndReplaysItsFixtures(t *testing.T) {
	dir, e := removeAlpha(t)

	result, err := RestoreArchived(ProjectScope, e)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := manifest.Hooks["alpha"]
	if !ok || entry.Rule != "block alpha" || entry.Source != SourceManual {
		t.Fatalf("restored manifest entry = %+v, %v", entry, ok)
	}
	guard, err := os.ReadFile(filepath.Join(dir, "alpha.py"))
	if err != nil || !strings.Contains(string(guard), `TOKEN = "ALPHA"`) {
		t.Fatalf("restored guard = %q, %v", guard, err)
	}
	if _, found, err := fixture.Load(dir, "alpha"); !found || err != nil {
		t.Fatalf("the fixtures were not restored: %v", err)
	}
	if result.Verification == nil || len(result.Verification.Reports) != 1 || result.Verification.Failed() {
		t.Fatalf("verification = %+v; want the one guard's fixtures replayed and passing", result.Verification)
	}
	if _, err := os.Stat(e.Dir); err != nil {
		t.Fatalf("the archive entry did not stay: %v", err)
	}
}

func TestRestoreRefusesAGuardIDTheStoreHoldsNow(t *testing.T) {
	_, e := removeAlpha(t)
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "alpha", Rule: "block beta", Content: []byte(blockingHook("BETA")),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreArchived(ProjectScope, e); err == nil {
		t.Fatal("a restore overwrote the installed guard")
	}
	manifest, _ := LoadManifest(ProjectScope)
	if manifest.Hooks["alpha"].Rule != "block beta" {
		t.Fatalf("installed guard now carries %q", manifest.Hooks["alpha"].Rule)
	}
}

func TestRestoreRefusesAGuardFileWithNoManifestEntry(t *testing.T) {
	dir, e := removeAlpha(t)
	orphan := filepath.Join(dir, "alpha.py")
	if err := os.WriteFile(orphan, []byte(blockingHook("ORPHAN")), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreArchived(ProjectScope, e); err == nil {
		t.Fatal("a restore overwrote a guard file that no manifest entry names")
	}
	content, _ := os.ReadFile(orphan)
	if !strings.Contains(string(content), "ORPHAN") {
		t.Fatal("the orphan guard file was overwritten")
	}
}

func TestRestoreRefusesAGuardEntryMissingAPart(t *testing.T) {
	dir := fixtureProject(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte(blockingHook("ALPHA")), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)
	if len(e.Missing) != 1 || e.Missing[0] != ArchiveMissingManifest {
		t.Fatalf("missing = %v; want the manifest entry", e.Missing)
	}

	if _, err := RestoreArchived(ProjectScope, e); err == nil || !strings.Contains(err.Error(), ArchiveMissingManifest) {
		t.Fatalf("an entry with no manifest entry was restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.py")); !os.IsNotExist(err) {
		t.Fatal("a refused restore wrote the guard file")
	}
}

func TestRestoreReinstallsARuleAndRefusesOneInPlace(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallManagedRule(ProjectScope, "config-path", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedRule(ProjectScope, "config-path", ""); err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)

	if _, err := RestoreArchived(ProjectScope, e); err != nil {
		t.Fatal(err)
	}
	manifest, _ := LoadManifest(ProjectScope)
	if manifest.Rules["config-path"].Text != "resolve a config path from the repo root" {
		t.Fatalf("restored rule = %+v", manifest.Rules["config-path"])
	}

	if _, err := InstallManagedRule(ProjectScope, "config-path", "a reworded rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreArchived(ProjectScope, e); err == nil {
		t.Fatal("a restore overwrote the rule in place")
	}
	manifest, _ = LoadManifest(ProjectScope)
	if manifest.Rules["config-path"].Text != "a reworded rule" {
		t.Fatalf("rule in place now reads %q", manifest.Rules["config-path"].Text)
	}
}

func TestRestoreReinstallsAnAddendumEntryAndRefusesOneInPlace(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallAddendum(ProjectScope, "no-style", "style is not a finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveAddendum(ProjectScope, "no-style", ""); err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)

	if _, err := RestoreArchived(ProjectScope, e); err != nil {
		t.Fatal(err)
	}
	manifest, _ := LoadManifest(ProjectScope)
	if manifest.Addendum["no-style"].Text != "style is not a finding" {
		t.Fatalf("restored entry = %+v", manifest.Addendum["no-style"])
	}

	if _, err := InstallAddendum(ProjectScope, "no-style", "formatting is not a finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreArchived(ProjectScope, e); err == nil {
		t.Fatal("a restore overwrote the addendum entry in place")
	}
}

func TestRestoreLeavesAnIDAnotherTierHoldsToTheInstaller(t *testing.T) {
	withProjectTempDir(t)
	if _, err := InstallManagedRule(ProjectScope, "shared", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedRule(ProjectScope, "shared", ""); err != nil {
		t.Fatal(err)
	}
	e := onlyEntry(t)
	if _, err := InstallAddendum(ProjectScope, "shared", "style is not a finding"); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreArchived(ProjectScope, e); err == nil || !strings.Contains(err.Error(), "review addendum entry") {
		t.Fatalf("a rule was restored over an addendum entry's id: %v", err)
	}
}
