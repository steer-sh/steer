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

func installGeneratedHook(t *testing.T, scope, hookName, rule string) {
	t.Helper()
	_, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: hookName,
		FileName: hookName + ".py",
		Rule:     rule,
		Content:  []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == \"__main__\":\n    main()\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook %s returned error: %v", hookName, err)
	}
}

func lockCurrentState(t *testing.T) *teamLockfile {
	t.Helper()
	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}
	lockfile, err := buildTeamLockfile(claude.ProjectScope, catalog)
	if err != nil {
		t.Fatalf("buildTeamLockfile returned error: %v", err)
	}
	return lockfile
}

func requireNoFindings(t *testing.T, lockfile *teamLockfile) {
	t.Helper()
	findings, err := verifyAgainstLockfile(claude.ProjectScope, lockfile)
	if err != nil {
		t.Fatalf("verifyAgainstLockfile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got: %v", findings)
	}
}

func TestTeamLockDeterministicAndSorted(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	installGeneratedHook(t, claude.ProjectScope, "zz-team-rule", "no team rule violations")

	first := lockCurrentState(t)
	second := lockCurrentState(t)

	if err := writeTeamLockfile("first.lock.json", first); err != nil {
		t.Fatalf("writeTeamLockfile returned error: %v", err)
	}
	if err := writeTeamLockfile("second.lock.json", second); err != nil {
		t.Fatalf("writeTeamLockfile returned error: %v", err)
	}
	firstData, _ := os.ReadFile("first.lock.json")
	secondData, _ := os.ReadFile("second.lock.json")
	if string(firstData) != string(secondData) {
		t.Fatalf("expected deterministic lockfile output")
	}

	names := make([]string, 0, len(first.Hooks))
	for _, hook := range first.Hooks {
		names = append(names, hook.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("expected sorted hook names, got %v", names)
	}
	if len(first.Hooks) != 3 {
		t.Fatalf("expected 3 locked hooks (visible + internal + generated), got %d", len(first.Hooks))
	}
}

func TestTeamApplyFreshClone(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	installGeneratedHook(t, claude.ProjectScope, "team-rule", "no team rule violations in fresh clone")
	lockfile := lockCurrentState(t)

	// Fresh clone: a second temp dir with only the lockfile.
	withCmdProjectTempDir(t)
	if err := writeTeamLockfile(TeamLockfileName, lockfile); err != nil {
		t.Fatalf("writeTeamLockfile returned error: %v", err)
	}
	reread, err := readTeamLockfile(TeamLockfileName)
	if err != nil {
		t.Fatalf("readTeamLockfile returned error: %v", err)
	}

	result, err := applyTeamLockfile(claude.ProjectScope, reread, false, false)
	if err != nil {
		t.Fatalf("applyTeamLockfile returned error: %v", err)
	}

	installedCount := 0
	for _, outcome := range result.Outcomes {
		if outcome.Status == claude.OutcomeInstalled {
			installedCount++
			if outcome.Drift != claude.DriftMissingEntry {
				t.Fatalf("expected missing_entry drift on fresh clone, got %s for %s", outcome.Drift, outcome.HookName)
			}
		}
	}
	if installedCount != len(reread.Hooks) {
		t.Fatalf("expected %d installs, got %d", len(reread.Hooks), installedCount)
	}

	requireNoFindings(t, reread)

	claudeMDPath, err := claude.GetClaudeMDFile(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetClaudeMDFile returned error: %v", err)
	}
	claudeMD, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md after apply: %v", err)
	}
	if !strings.Contains(string(claudeMD), "no team rule violations in fresh clone") {
		t.Fatalf("expected locked rule in CLAUDE.md after apply")
	}
}

func TestTeamApplyFloorKeepsPersonalRemovesFactoryExtras(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	lockfile := lockCurrentState(t)

	// Personal hook outside the lockfile plus a factory extra outside it.
	installGeneratedHook(t, claude.ProjectScope, "personal-hook", "my personal rule")
	addOrphanFactoryHook(t, claude.ProjectScope, "factory-extra", "gone-from-lock", "factory extra rule")

	if _, err := applyTeamLockfile(claude.ProjectScope, lockfile, false, false); err != nil {
		t.Fatalf("applyTeamLockfile returned error: %v", err)
	}

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if _, ok := manifest.Hooks["personal-hook"]; !ok {
		t.Fatalf("expected personal hook to survive floor apply")
	}
	if _, ok := manifest.Hooks["factory-extra"]; ok {
		t.Fatalf("expected factory extra to be removed by floor apply")
	}
}

func TestTeamApplyForceRemovesAllExtras(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	lockfile := lockCurrentState(t)

	installGeneratedHook(t, claude.ProjectScope, "personal-hook", "my personal rule")

	if _, err := applyTeamLockfile(claude.ProjectScope, lockfile, false, true); err != nil {
		t.Fatalf("applyTeamLockfile returned error: %v", err)
	}

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if _, ok := manifest.Hooks["personal-hook"]; ok {
		t.Fatalf("expected personal hook removed by --force apply")
	}
	requireNoFindings(t, lockfile)
}

func TestTeamApplyDryRunDoesNotMutate(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	lockfile := lockCurrentState(t)

	withCmdProjectTempDir(t)
	result, err := applyTeamLockfile(claude.ProjectScope, lockfile, true, false)
	if err != nil {
		t.Fatalf("applyTeamLockfile dry-run returned error: %v", err)
	}
	if result.Planned != len(lockfile.Hooks) {
		t.Fatalf("expected %d planned mutations, got %d", len(lockfile.Hooks), result.Planned)
	}
	if _, err := os.Stat(".claude"); !os.IsNotExist(err) {
		t.Fatalf("expected dry-run apply to leave no .claude directory, stat err=%v", err)
	}
}

func TestTeamVerifyFindsDrift(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	lockfile := lockCurrentState(t)
	requireNoFindings(t, lockfile)

	hooksDir, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	path := filepath.Join(hooksDir, visible.HookFile)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n# tampered\n"), 0o755); err != nil {
		t.Fatalf("failed to tamper hook file: %v", err)
	}

	findings, err := verifyAgainstLockfile(claude.ProjectScope, lockfile)
	if err != nil {
		t.Fatalf("verifyAgainstLockfile returned error: %v", err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "reason=content") {
		t.Fatalf("expected single content-drift finding, got: %v", findings)
	}

	// Apply repairs the drift.
	if _, err := applyTeamLockfile(claude.ProjectScope, lockfile, false, false); err != nil {
		t.Fatalf("applyTeamLockfile returned error: %v", err)
	}
	requireNoFindings(t, lockfile)
}

func TestReadTeamLockfileRejectsTamper(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
	installVisibleAndInternal(t, claude.ProjectScope, catalog, visible)
	lockfile := lockCurrentState(t)

	lockfile.Hooks[0].Rule = "tampered rule without updated hash"
	if err := writeTeamLockfile(TeamLockfileName, lockfile); err != nil {
		t.Fatalf("writeTeamLockfile returned error: %v", err)
	}
	if _, err := readTeamLockfile(TeamLockfileName); err == nil || !strings.Contains(err.Error(), "rule_hash mismatch") {
		t.Fatalf("expected rule_hash integrity error, got: %v", err)
	}

	lockfile = lockCurrentState(t)
	lockfile.Hooks[0].Content += "\n# tampered\n"
	if err := writeTeamLockfile(TeamLockfileName, lockfile); err != nil {
		t.Fatalf("writeTeamLockfile returned error: %v", err)
	}
	if _, err := readTeamLockfile(TeamLockfileName); err == nil || !strings.Contains(err.Error(), "content_sha256 mismatch") {
		t.Fatalf("expected content_sha256 integrity error, got: %v", err)
	}
}
