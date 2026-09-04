package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/presets"
)

const guardSource = `#!/usr/bin/env python3
import json
import sys


def main():
    data = json.load(sys.stdin)
    if "var " in data.get("tool_input", {}).get("content", ""):
        print(json.dumps({"decision": "block", "stopReason": "never use var"}))
        sys.exit(0)


main()
`

func bothHarnesses() []harness.Registrar {
	return []harness.Registrar{claude.Registrar{}, codex.Registrar{}}
}

func installGuard(t *testing.T, targets ...harness.Registrar) {
	t.Helper()
	if _, err := claude.InstallManagedHook(claude.ProjectScope, claude.InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  []byte(guardSource),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}, targets...); err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(raw)
}

func TestOneStoreServesBothHarnessRegistrations(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, bothHarnesses()...)

	// One store: the guard and the master are written once, not per harness.
	dir, err := hookstore.Dir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	master := readFile(t, filepath.Join(dir, hookstore.MasterName))
	if !strings.Contains(master, "never use var") {
		t.Error("the master does not carry the guard")
	}

	// Two registrations, each in its own harness's configuration.
	settings := readFile(t, filepath.Join(".claude", "settings.json"))
	if !strings.Contains(settings, hookstore.MasterName) {
		t.Error("Claude Code settings.json does not invoke the master")
	}
	if strings.Contains(settings, "--harness") {
		t.Error("Claude Code's registration carries a harness flag; its contract is the default")
	}
	hooksJSON := readFile(t, filepath.Join(".codex", "hooks.json"))
	if !strings.Contains(hooksJSON, "--harness codex") {
		t.Error("the Codex registration does not select the Codex reply contract")
	}
	if !strings.Contains(hooksJSON, codex.MasterMatcher) {
		t.Errorf("the Codex registration does not match %s", codex.MasterMatcher)
	}

	// Two instruction documents, each the one its harness reads.
	for _, path := range []string{"CLAUDE.md", "AGENTS.md"} {
		if doc := readFile(t, path); !strings.Contains(doc, "- never use var") {
			t.Errorf("%s is missing the rule", path)
		}
	}
}

func TestRemovingFromBothHarnessesLeavesNoRegistration(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, bothHarnesses()...)

	if _, err := claude.RemoveManagedHook(claude.ProjectScope, "no-var-hook", bothHarnesses()...); err != nil {
		t.Fatalf("RemoveManagedHook returned error: %v", err)
	}

	for _, target := range bothHarnesses() {
		registered, err := target.MasterRegistered(claude.ProjectScope)
		if err != nil {
			t.Fatal(err)
		}
		if registered {
			t.Errorf("%s still invokes the master after the last guard was removed", target.Harness().ID)
		}
		path, err := target.InstructionPath(claude.ProjectScope)
		if err != nil {
			t.Fatal(err)
		}
		if doc := readFile(t, path); strings.Contains(doc, "- never use var") {
			t.Errorf("%s still carries the removed rule", path)
		}
	}
}

func TestRegisteringOneHarnessLeavesTheOtherUntouched(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, codex.Registrar{})

	if _, err := os.Stat(filepath.Join(".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("a Codex-only install wrote Claude Code's settings.json")
	}
	if _, err := os.Stat("CLAUDE.md"); !os.IsNotExist(err) {
		t.Error("a Codex-only install wrote CLAUDE.md")
	}
	if doc := readFile(t, "AGENTS.md"); !strings.Contains(doc, "- never use var") {
		t.Error("AGENTS.md is missing the rule")
	}
}

func TestNoNamedHarnessRegistersClaudeCode(t *testing.T) {
	withCmdProjectTempDir(t)
	// The default now follows the harnesses installed on the machine, so the
	// fallback is only reachable from a home holding none.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("STEER_HARNESS", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_SESSION_ID", "")
	harnessFlag, allHarnesses = "", false

	targets, err := currentRegistrars()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Harness().ID != harness.ClaudeCode {
		t.Fatalf("targets = %v; an ordinary shell keeps writing to Claude Code", harnessNames(targets))
	}
}

func TestAllHarnessesTargetsEveryHarnessThatRegistersHooks(t *testing.T) {
	harnessFlag, allHarnesses = "", true
	t.Cleanup(func() { allHarnesses = false })

	targets, err := currentRegistrars()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %v; want both harnesses", harnessNames(targets))
	}
}

func TestAnUnknownHarnessIsRefused(t *testing.T) {
	harnessFlag, allHarnesses = "emacs", false
	t.Cleanup(func() { harnessFlag = "" })

	if _, err := currentRegistrars(); err == nil {
		t.Error("currentRegistrars accepted an unregistered harness")
	}
}

// The guard store is shared, so every idempotence check that reads only the store
// reports a hook as installed that a second harness was never registered for.
func TestAddingASecondHarnessToAnInstalledGuardStillRegistersIt(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, claude.Registrar{})
	// The store is now complete. Only Codex's registration and rules are missing.
	installGuard(t, codex.Registrar{})

	if hooks := readFile(t, filepath.Join(".codex", "hooks.json")); !strings.Contains(hooks, "--harness codex") {
		t.Error("the second harness never got the registration")
	}
	if doc := readFile(t, "AGENTS.md"); !strings.Contains(doc, "- never use var") {
		t.Error("the second harness never got the rule")
	}
}

func TestDriftCountsAHarnessThatCarriesNoRegistration(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, claude.Registrar{})

	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Hooks["no-var-hook"]
	desired := claude.DesiredHook{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  claude.ApplyMetadataHeader([]byte(guardSource), entry.Source, entry.PromptVersion, entry.HookVersion),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}

	drifted, _, err := claude.DesiredHookDrift(claude.ProjectScope, desired, entry, true, claude.Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if drifted {
		t.Error("the harness it was installed into reads as drifted")
	}

	drifted, reason, err := claude.DesiredHookDrift(claude.ProjectScope, desired, entry, true, codex.Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if !drifted {
		t.Fatal("a harness with no registration and no rule reads as up to date")
	}
	if reason != claude.DriftHarness {
		t.Errorf("reason = %q; want %q", reason, claude.DriftHarness)
	}
}

func TestEnablingAPresetForASecondHarnessIsNotAlreadyEnabled(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, item := loadCatalogAndOneVisiblePreset(t)

	if _, err := presets.EnablePreset(claude.ProjectScope, catalog, item, false, claude.Registrar{}); err != nil {
		t.Fatalf("EnablePreset returned error: %v", err)
	}
	status, err := presets.EnablePreset(claude.ProjectScope, catalog, item, false, codex.Registrar{})
	if err != nil {
		t.Fatalf("EnablePreset for the second harness returned error: %v", err)
	}
	if status == "already enabled" {
		t.Fatal("the second harness was reported enabled without being registered")
	}
	if hooks := readFile(t, filepath.Join(".codex", "hooks.json")); !strings.Contains(hooks, "--harness codex") {
		t.Error("the second harness never got the registration")
	}
}

func TestSyncReinstallsForAHarnessThatWasNeverRegistered(t *testing.T) {
	withCmdProjectTempDir(t)
	catalog, item := loadCatalogAndOneVisiblePreset(t)
	if _, err := presets.EnablePreset(claude.ProjectScope, catalog, item, false, claude.Registrar{}); err != nil {
		t.Fatalf("EnablePreset returned error: %v", err)
	}

	result, err := presets.SyncFactoryPresets(claude.ProjectScope, catalog, presets.SyncOptions{}, codex.Registrar{})
	if err != nil {
		t.Fatalf("SyncFactoryPresets returned error: %v", err)
	}
	if result.Reinstalled == 0 {
		t.Fatal("sync found nothing to do for a harness with no registration")
	}
	if hooks := readFile(t, filepath.Join(".codex", "hooks.json")); !strings.Contains(hooks, "--harness codex") {
		t.Error("sync did not register the master for the second harness")
	}
}

func TestTeamVerifyChecksTheTargetHarnessNotAlwaysClaudeCode(t *testing.T) {
	withCmdProjectTempDir(t)
	installGuard(t, codex.Registrar{})

	lockfile := &teamLockfile{Hooks: []teamLockedHook{{
		Name:          "no-var-hook",
		File:          "no-var-hook.py",
		Rule:          "never use var",
		Source:        claude.SourceManual,
		PromptVersion: claude.DefaultPromptVersion,
		HookVersion:   claude.DefaultHookVersion,
		RuleHash:      claude.HashRule("never use var"),
	}}}
	dir, err := hookstore.Dir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "no-var-hook.py"))
	if err != nil {
		t.Fatal(err)
	}
	lockfile.Hooks[0].Content = string(content)
	lockfile.Hooks[0].ContentSHA256 = claude.ContentSHA256(content)

	findings, err := verifyAgainstLockfile(claude.ProjectScope, lockfile, codex.Registrar{})
	if err != nil {
		t.Fatalf("verifyAgainstLockfile returned error: %v", err)
	}
	for _, finding := range findings {
		if strings.Contains(finding, "reason=harness") {
			t.Errorf("verify reported %q against a harness that is fully registered", finding)
		}
	}
}
