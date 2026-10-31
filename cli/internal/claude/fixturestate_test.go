package claude

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/steer-sh/steer-cli/internal/guard/fixture"
)

// verified sets up a project store with one guard, saves cases, and runs the
// verification that earns the binding.
func verified(t *testing.T, cases ...fixture.Case) string {
	t.Helper()
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := fixture.Save(dir, fixture.File{Version: fixture.Version, Hook: "alpha", Cases: cases}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyHookFixtures(ProjectScope, "alpha"); err != nil {
		t.Fatal(err)
	}
	if b := binding(t, "alpha"); b == nil || !b.Tested {
		t.Fatalf("the guard did not earn a pass: %+v", b)
	}
	return dir
}

func snapshot(t *testing.T) *FixtureSnapshot {
	t.Helper()
	s, err := ReadHookFixtureSnapshot(ProjectScope, "alpha")
	if err != nil {
		t.Fatalf("ReadHookFixtureSnapshot: %v", err)
	}
	if s == nil {
		t.Fatal("a registered guard yielded no snapshot")
	}
	return s
}

// The digest is taken over the encoded document, so a reader that reloads the
// stored file must reach the receipt's own value. Reformatting the file changes
// its bytes and not its content; changing a payload changes its content.
func TestAReloadedFixtureFileRecomputesTheReceiptsOwnDigest(t *testing.T) {
	dir := verified(t,
		bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock),
		bashCase("allows other work", "run BETA now", fixture.ExpectAllow),
	)
	s := snapshot(t)
	if len(s.Changed) != 0 || len(s.Unavailable) != 0 {
		t.Fatalf("a freshly verified guard is not current: changed=%v unavailable=%v", s.Changed, s.Unavailable)
	}

	path := fixture.Path(dir, "alpha")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compact, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := snapshot(t); len(s.Changed) != 0 {
		t.Errorf("reformatting the stored file changed the digest: %v", s.Changed)
	}

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var file fixture.File
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	file.Cases[0].Payload["tool_input"] = map[string]any{"command": "run ALPHA later"}
	if err := fixture.Save(dir, file); err != nil {
		t.Fatal(err)
	}
	if s := snapshot(t); !namesComponent(s.Changed, FixtureComponentFixtures) {
		t.Errorf("a changed payload left the fixtures digest alone: changed=%v", s.Changed)
	}
}

// A guard file that is gone is unreadable, not rewritten. Reporting it as a
// changed digest would tell the operator to re-verify a guard that is not there.
func TestAnUnreadableComponentIsUnavailableAndNotChanged(t *testing.T) {
	dir := verified(t, bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock))
	if err := os.Remove(fixture.Path(dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	s := snapshot(t)
	if !namesComponent(s.Unavailable, FixtureComponentFixtures) {
		t.Errorf("the missing fixtures are not unavailable: %+v", s)
	}
	if namesComponent(s.Changed, FixtureComponentFixtures) {
		t.Errorf("the missing fixtures were reported as a rewrite: %v", s.Changed)
	}
	if s.Reason == "" {
		t.Error("an unavailable component carries no reason")
	}
}

// Consolidation clears Tested and keeps the digests, so a reader must not read
// the retained digests back as a pass.
func TestAClearedPassReadsAsNoRetainedPass(t *testing.T) {
	verified(t, bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock))
	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	s := snapshot(t)
	if s.Binding == nil || s.Binding.Tested {
		if s.Binding != nil && s.Binding.Digest == "" {
			t.Fatal("consolidation dropped the digests as well as the state")
		}
	}
	if s.Binding.Tested {
		t.Fatal("the pass survived consolidation")
	}
	if len(s.Changed) != 0 || len(s.Unavailable) != 0 {
		t.Errorf("a cleared pass was compared anyway: changed=%v unavailable=%v", s.Changed, s.Unavailable)
	}
}

// A pass earned with no master names no master identity, so it cannot be read as
// current under the master the guard runs beneath now.
func TestAPassWithNoMasterIdentityIsNotCurrent(t *testing.T) {
	verified(t, bashCase("blocks the token", "run ALPHA now", fixture.ExpectBlock))
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Hooks["alpha"]
	entry.Fixtures.MasterDigest = ""
	if err := SetHookEntry(ProjectScope, "alpha", entry); err != nil {
		t.Fatal(err)
	}
	if s := snapshot(t); !s.NoMasterIdentity {
		t.Errorf("a pass with no master digest read as an ordinary comparison: %+v", s)
	}
}

// An unregistered hook and an absent store are absence, not failure.
func TestAnAbsentHookYieldsNoSnapshot(t *testing.T) {
	fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	s, err := ReadHookFixtureSnapshot(ProjectScope, "beta")
	if err != nil {
		t.Fatalf("an unregistered hook is not an error: %v", err)
	}
	if s != nil {
		t.Errorf("an unregistered hook yielded a snapshot: %+v", s)
	}
}

func TestChangedNamesEveryComponentThatMoved(t *testing.T) {
	b := &FixtureBinding{Tested: true, Digest: "f", HookDigest: "h", RuleHash: "r", MasterDigest: "m"}
	got := b.Changed("f2", "h", "r2", "m")
	want := []string{FixtureComponentFixtures, FixtureComponentRule}
	if len(got) != len(want) {
		t.Fatalf("Changed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Changed = %v, want %v", got, want)
		}
	}
	if b.Bound("f2", "h", "r2", "m") {
		t.Error("Bound accepted an identity two components moved under")
	}
	if !b.Bound("f", "h", "r", "m") {
		t.Error("Bound refused the identity the state was earned under")
	}
}
