package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// blockingHook blocks any Bash command holding the token it is built around.
func blockingHook(token string) string {
	return `#!/usr/bin/env python3
import json
import sys

TOKEN = "` + token + `"

def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        sys.exit(0)
    if data.get("tool_name") != "Bash":
        sys.exit(0)
    command = data.get("tool_input", {}).get("command", "")
    if TOKEN in command:
        print(json.dumps({"decision": "block", "continue": True, "stopReason": "blocked " + TOKEN}))
    sys.exit(0)

if __name__ == "__main__":
    main()
`
}

func bashCase(name, command, expect string) fixture.Case {
	return fixture.Case{
		Name:   name,
		Expect: expect,
		Origin: fixture.OriginSynthetic,
		Payload: map[string]any{
			"tool_name":  "Bash",
			"tool_input": map[string]any{"command": command},
		},
	}
}

// fixtureProject sets up a project store holding the named guards and returns
// the store directory. Every guard is registered, so the master carries it.
func fixtureProject(t *testing.T, guards map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := EnsureDirectories(ProjectScope); err != nil {
		t.Fatal(err)
	}
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range guards {
		if err := os.WriteFile(filepath.Join(hooksDir, name+".py"), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SetHookEntry(ProjectScope, name, ManifestEntry{
			File:     name + ".py",
			Rule:     "block " + name,
			Source:   SourceManual,
			RuleHash: HashRule("block " + name),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	return hooksDir
}

func binding(t *testing.T, hook string) *FixtureBinding {
	t.Helper()
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Hooks[hook].Fixtures
}

func TestAGuardEarnsTestedFromItsStoredCases(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
		bashCase("allows other work", "run BETA now", fixture.ExpectAllow),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "alpha")
	if err != nil {
		t.Fatalf("VerifyHookFixtures: %v", err)
	}
	if !report.Tested() {
		t.Fatalf("a guard that answers both cases is not tested: %+v", report.Cases)
	}
	if b := binding(t, "alpha"); b == nil || !b.Tested || b.Cases != 2 {
		t.Fatalf("binding is %+v", b)
	}
}

func TestAOneByteGuardEditDropsTheTestedState(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyHookFixtures(ProjectScope, "alpha"); err != nil {
		t.Fatal(err)
	}
	before := binding(t, "alpha")
	if before == nil || !before.Tested {
		t.Fatal("the guard did not earn tested")
	}

	path := filepath.Join(dir, "alpha.py")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(source, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}

	if before.Bound(before.Digest, fixture.FileDigest(path), before.RuleHash, before.MasterDigest) {
		t.Fatal("the state survived a one-byte guard edit")
	}
}

func TestARewordedRuleDropsTheTestedState(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyHookFixtures(ProjectScope, "alpha"); err != nil {
		t.Fatal(err)
	}
	b := binding(t, "alpha")
	// A code edit leaves rule_hash alone, so the guard's own digest cannot carry
	// the binding: a reworded rule changes what the guard is meant to do.
	if b.Bound(b.Digest, b.HookDigest, HashRule("a different rule"), b.MasterDigest) {
		t.Fatal("the state survived a reworded rule")
	}
}

func TestConsolidationClearsTheTestedStateAndTheRerunReEstablishesIt(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyHookFixtures(ProjectScope, "alpha"); err != nil {
		t.Fatal(err)
	}

	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if b := binding(t, "alpha"); b == nil || b.Tested {
		t.Fatalf("consolidation left the state standing: %+v", b)
	}

	verification, err := VerifyFixtures(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Failed() {
		t.Fatal("the rerun did not re-establish the state")
	}
	if b := binding(t, "alpha"); b == nil || !b.Tested {
		t.Fatalf("the rerun left the state cleared: %+v", b)
	}
}

func TestACaseAnotherGuardCatchesFirstIsShadowedAndNeverAFailure(t *testing.T) {
	// Both guards block the same command. Guards run guard-major and the first
	// to object wins, so whichever the master lists first owns the audit row.
	dir := fixtureProject(t, map[string]string{
		"alpha": blockingHook("SHARED"),
		"beta":  blockingHook("SHARED"),
	})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "beta", Cases: []fixture.Case{
		bashCase("blocks the shared token", "run SHARED now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "beta")
	if err != nil {
		t.Fatal(err)
	}
	row := report.Cases[0]
	if row.MasterState != fixture.MasterShadowed {
		t.Fatalf("want shadowed, got %s (objector %q)", row.MasterState, row.MasterObjector)
	}
	if row.MasterObjector != "alpha" {
		t.Fatalf("the objector is %q", row.MasterObjector)
	}
	if !row.IndividualPass {
		t.Fatal("the guard failed its own case")
	}
	if report.MasterFailures() != 0 {
		t.Fatal("a shadowed case counted as a master failure")
	}
	// Shadowing is unverified integration, not a failure: the individual pass
	// is the guard's whole contract and it held. The count is reported so that
	// nobody reads aggregate interception as proof this guard executed.
	if !report.Tested() {
		t.Fatal("a shadowed case cost the guard its own passing contract")
	}
	if report.Shadowed() != 1 {
		t.Fatalf("the shadow is not reported: %d", report.Shadowed())
	}
	if b := binding(t, "beta"); b == nil || b.Shadowed != 1 {
		t.Fatalf("the binding does not carry the shadow: %+v", b)
	}
}

func TestAMasterPositiveThatAllowsIsAFailure(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	// The guard file is edited after consolidation, so the master still carries
	// the old source. This is the integration the master pass exists to catch.
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte(blockingHook("GAMMA")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the new token", "run GAMMA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	row := report.Cases[0]
	if !row.IndividualPass {
		t.Fatal("the edited guard failed its own case")
	}
	if row.MasterState != fixture.MasterFailed {
		t.Fatalf("want a master failure, got %s", row.MasterState)
	}
	if report.Tested() {
		t.Fatal("a case the master allowed earned tested")
	}
}

func TestFixtureRunsLeaveTheLiveAuditLogUntouched(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	auditPath := filepath.Join(dir, "steer-audit.jsonl")
	existing := []byte(`{"hook_name":"alpha","enforcement":"block"}` + "\n")
	if err := os.WriteFile(auditPath, existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
		bashCase("blocks it again", "ALPHA twice", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := VerifyHookFixtures(ProjectScope, "alpha"); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(existing) {
		t.Fatalf("the fixture run wrote to the live audit log:\n%s", after)
	}
}

func TestAFailingFixtureNeverBlocksARemoval(t *testing.T) {
	dir := fixtureProject(t, map[string]string{
		"alpha": blockingHook("ALPHA"),
		"beta":  blockingHook("BETA"),
	})
	// beta's case asserts a block the guard does not make, so it fails.
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "beta", Cases: []fixture.Case{
		bashCase("asserts a block it never makes", "run NOTHING now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}

	result, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{})
	if err != nil {
		t.Fatalf("a failing fixture on another guard blocked the removal: %v", err)
	}
	if result.HookName != "alpha" {
		t.Fatalf("removed %s", result.HookName)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.py")); !os.IsNotExist(err) {
		t.Fatal("the guard file survived the removal")
	}
	if result.Verification == nil || !result.Verification.Failed() {
		t.Fatal("the removal did not report the failing fixture")
	}
}

func TestRemovingAGuardDropsItsFixtures(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.Path(dir, "alpha")); !os.IsNotExist(err) {
		t.Fatal("the fixtures outlived the guard")
	}
}

func TestInstallStoresFixturesAndRebuildRerunsThem(t *testing.T) {
	withProjectTempDir(t)

	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "alpha",
		Rule:     "block ALPHA",
		Content:  []byte(blockingHook("ALPHA")),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
		Fixtures: &fixture.File{Version: fixture.Version, Cases: []fixture.Case{
			bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
		}},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook: %v", err)
	}
	if result.Verification == nil || result.Verification.Failed() {
		t.Fatalf("install did not verify the stored fixtures: %+v", result.Verification)
	}
	if b := binding(t, "alpha"); b == nil || !b.Tested {
		t.Fatalf("install left no state: %+v", b)
	}

	rebuilt, err := RebuildMaster(ProjectScope)
	if err != nil {
		t.Fatalf("RebuildMaster: %v", err)
	}
	if rebuilt.Verification == nil || len(rebuilt.Verification.Reports) != 1 {
		t.Fatalf("rebuild did not rerun the fixtures: %+v", rebuilt.Verification)
	}
	// The rebuild wrote a new master, so the state it re-earns names that one.
	b := binding(t, "alpha")
	if b == nil || !b.Tested {
		t.Fatalf("rebuild left the state cleared: %+v", b)
	}
	if b.MasterDigest != fixture.FileDigest(rebuilt.MasterPath) {
		t.Fatal("the state names a master it did not run under")
	}
}

func TestAnAllowCaseThisGuardBlocksThroughTheMasterIsNotTested(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	// The store file is edited after consolidation, so the guard alone allows
	// ALPHA while the source the master carries still blocks it. Before this
	// reading the run named no objector and explained the block away.
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte(blockingHook("GAMMA")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("leaves the old token alone", "run ALPHA now", fixture.ExpectAllow),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	row := report.Cases[0]
	if !row.IndividualPass {
		t.Fatal("the edited guard failed its own case")
	}
	if row.MasterState != fixture.MasterBlockedAllow {
		t.Fatalf("want a blocked allow case, got %s (objector %q)", row.MasterState, row.MasterObjector)
	}
	if row.MasterObjector != "alpha" {
		t.Fatalf("the objector is %q", row.MasterObjector)
	}
	if report.BlockedAllow() != 1 {
		t.Fatalf("the violation is not counted: %d", report.BlockedAllow())
	}
	if report.Tested() {
		t.Fatal("a case this guard blocked against its own label earned tested")
	}
	// The accounting names it. A summary of individual and master-allowed
	// failures alone reports this run as NOT verified with two zeros.
	if !strings.Contains(report.NotVerified("master"), "labelled allow") {
		t.Fatalf("the summary does not name the violation: %s", report.NotVerified("master"))
	}
}

func TestAnAllowCaseAnotherGuardBlocksNamesThatGuardAndStaysTested(t *testing.T) {
	dir := fixtureProject(t, map[string]string{
		"alpha": blockingHook("SHARED"),
		"beta":  blockingHook("NOTHING-ELSE"),
	})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "beta", Cases: []fixture.Case{
		bashCase("beta allows the shared token", "run SHARED now", fixture.ExpectAllow),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "beta")
	if err != nil {
		t.Fatal(err)
	}
	row := report.Cases[0]
	if row.MasterState != fixture.MasterNotRun {
		t.Fatalf("another guard's block is not this guard's violation: %s", row.MasterState)
	}
	if row.MasterObjector != "alpha" {
		t.Fatalf("the objector is not named: %q", row.MasterObjector)
	}
	if !report.Tested() {
		t.Fatal("another guard blocking an allow case cost this guard its contract")
	}
}

func TestAnAllowCaseWithNoMasterReachesNoAnswer(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(master); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("leaves other commands alone", "run GAMMA now", fixture.ExpectAllow),
	}}); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyHookFixtures(ProjectScope, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	// An allow case with no master reached no answer, exactly as a block case
	// does. Otherwise a run that could not decide earns tested while a decided
	// but unattributed one does not.
	if report.Cases[0].MasterState != fixture.MasterUnverified {
		t.Fatalf("want unverified, got %s", report.Cases[0].MasterState)
	}
	if report.Tested() {
		t.Fatal("a case with no master to run against earned tested")
	}
	if report.Unanswered() != 1 {
		t.Fatalf("the unanswered case is not counted: %d", report.Unanswered())
	}
}

// A fixtures directory the repository carries as a symlink is refused, so no
// fixture file lands at its target.
func TestFixtureSaveRefusesALinkedFixturesDirectory(t *testing.T) {
	withProjectTempDir(t)
	if err := EnsureDirectories(ProjectScope); err != nil {
		t.Fatal(err)
	}
	dir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, fixture.DirName)); err != nil {
		t.Fatal(err)
	}

	err = fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
	}})
	if err == nil {
		t.Fatal("fixture.Save wrote through a linked fixtures directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "alpha.json")); !os.IsNotExist(err) {
		t.Fatalf("a fixture landed outside the store: %v", err)
	}
}

// A guard's stderr reaches a real master's stderr beside its deny on stdout. The
// master pass still reads the block, for both labels a case can carry.
func TestFixtureMasterPassReadsADenyPastStderr(t *testing.T) {
	noisy := strings.Replace(blockingHook("BANNED"), "def main():\n", "def main():\n    sys.stderr.write(\"guard diagnostic\\n\")\n", 1)
	store := fixtureProject(t, map[string]string{"noisy": noisy})
	guardPath := filepath.Join(store, "noisy.py")

	report, err := (fixture.Runner{}).Run(guardPath, filepath.Join(store, MasterHookName), fixture.File{
		Version: fixture.Version, Hook: "noisy", Cases: []fixture.Case{
			bashCase("expected block", "echo BANNED", fixture.ExpectBlock),
			bashCase("labelled allow", "echo BANNED", fixture.ExpectAllow),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Cases[0]; got.Master != fixture.OutcomeBlock || got.MasterState != fixture.MasterVerified {
		t.Errorf("expected block = %+v, want a block verified against this guard", got)
	}
	if got := report.Cases[1]; got.Master != fixture.OutcomeBlock || got.MasterState != fixture.MasterBlockedAllow {
		t.Errorf("labelled allow = %+v, want the master's block reported", got)
	}
}

func linkedSteerWithOldGuard(t *testing.T) string {
	t.Helper()
	withProjectTempDir(t)
	outside := t.TempDir()
	hooks := filepath.Join(outside, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{"old":{"file":"old.py","rule":"r"}}}`
	if err := os.WriteFile(filepath.Join(hooks, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Save(hooks, fixture.File{Version: fixture.Version, Hook: "old", Cases: []fixture.Case{
		bashCase("blocks", "x", fixture.ExpectBlock),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ".steer"); err != nil {
		t.Fatal(err)
	}
	return hooks
}

// A .steer the repository carries as a link is refused, so no fixture lands at
// its target.
func TestSaveFixturesRefusesALinkedSteerDirectory(t *testing.T) {
	hooks := linkedSteerWithOldGuard(t)
	err := SaveFixtures(ProjectScope, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: []fixture.Case{
		bashCase("blocks", "x", fixture.ExpectBlock),
	}})
	if err == nil {
		t.Error("SaveFixtures accepted a linked .steer")
	}
	if _, err := os.Stat(filepath.Join(hooks, fixture.DirName, "alpha.json")); err == nil {
		t.Error("SaveFixtures wrote through a linked .steer")
	}
}

// A bad document is refused before the store is created.
func TestSaveFixturesRefusesABadHookBeforeCreatingTheStore(t *testing.T) {
	withProjectTempDir(t)
	err := SaveFixtures(ProjectScope, fixture.File{Version: fixture.Version, Hook: "../x", Cases: []fixture.Case{
		bashCase("blocks", "x", fixture.ExpectBlock),
	}})
	if err == nil {
		t.Error("SaveFixtures accepted a hook that is not a guard name")
	}
	if _, err := os.Stat(".steer"); err == nil {
		t.Error("SaveFixtures created the store before refusing the document")
	}
}

// A refused rekey leaves a legacy store unmigrated.
func TestRekeyManagedHookRefusalLeavesALegacyStoreInPlace(t *testing.T) {
	withProjectTempDir(t)
	legacy, err := hookstore.LegacyDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{"old":{"file":"old.py","rule":"r"}}}`
	if err := os.WriteFile(filepath.Join(legacy, hookstore.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RekeyManagedHook(ProjectScope, "missing", "new", "new.py"); err == nil {
		t.Fatal("RekeyManagedHook accepted a missing source")
	}
	if _, err := os.Stat(".steer"); err == nil {
		t.Error("a refused rekey migrated the legacy store")
	}
}

// A rekey refuses a linked .steer before it writes the moved fixtures.
func TestRekeyManagedHookRefusesALinkedSteerDirectory(t *testing.T) {
	hooks := linkedSteerWithOldGuard(t)
	if err := RekeyManagedHook(ProjectScope, "old", "new", "new.py"); err == nil {
		t.Error("RekeyManagedHook accepted a linked .steer")
	}
	if _, err := os.Stat(filepath.Join(hooks, fixture.DirName, "new.json")); err == nil {
		t.Error("RekeyManagedHook wrote through a linked .steer")
	}
}
