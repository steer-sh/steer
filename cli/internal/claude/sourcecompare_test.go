package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAStoreEditDriftsFromTheMasterUntilARebuild(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}

	if c := CompareMasterSources(dir, master); !c.Agrees() {
		t.Fatalf("a freshly built master does not carry its store: %+v", c)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte(blockingHook("GAMMA")), 0o755); err != nil {
		t.Fatal(err)
	}
	c := CompareMasterSources(dir, master)
	if !c.Complete() {
		t.Fatalf("the comparison reached no answer: %+v", c)
	}
	if got := c.Drifted(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("the edited guard is not named as drifted: %v", got)
	}
	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if c := CompareMasterSources(dir, master); !c.Agrees() {
		t.Fatalf("a rebuild did not restore agreement: %+v", c)
	}
}

func TestTheComparisonSeparatesAnUnreadAnswerFromAgreement(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}

	// A master that is not there is a separate answer from one carrying the
	// store's guards. Neither agrees, and only one asks for a rebuild.
	kept, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(master); err != nil {
		t.Fatal(err)
	}
	missing := CompareMasterSources(dir, master)
	if missing.State != ComparisonNoMaster {
		t.Fatalf("a removed master reads as %s", missing.State)
	}
	if missing.Agrees() {
		t.Fatal("a comparison with no master agreed")
	}
	if len(missing.Drifted()) != 0 {
		t.Fatalf("an unread comparison named drifted guards: %v", missing.Drifted())
	}

	// A guard the reader cannot accept is unavailable, never absent: an empty
	// inventory would report the store as covered. The master goes back first,
	// so the comparison reaches the guard rather than stopping at a missing file.
	if err := os.WriteFile(master, kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte("def not_main():\n    pass\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unreadable := CompareMasterSources(dir, master)
	if unreadable.State != ComparisonUnavailable {
		t.Fatalf("an unreadable guard reads as %s", unreadable.State)
	}
	if !strings.Contains(unreadable.Reason, "alpha") {
		t.Fatalf("the reason does not name the guard: %q", unreadable.Reason)
	}
	if unreadable.Complete() {
		t.Fatal("a comparison that could not read a guard reported itself complete")
	}
	if len(unreadable.Drifted()) != 0 {
		t.Fatalf("an unreadable store named drifted guards: %v", unreadable.Drifted())
	}
}

func TestMasterCoversStillRefusesAnEditedStore(t *testing.T) {
	// pins existing behaviour
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := MasterCovers(ProjectScope); err != nil {
		t.Fatalf("a freshly built store is refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.py"), []byte(blockingHook("GAMMA")), 0o755); err != nil {
		t.Fatal(err)
	}
	err := MasterCovers(ProjectScope)
	if err == nil {
		t.Fatal("an edited store passed the refusal sync depends on")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("the store directory is not absolute")
	}
	if want := "alpha differs from its file"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal does not name the gap: %v", err)
	}
}

func TestAManifestChangeWithoutARebuildReadsAsStaleStamps(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA"), "beta": blockingHook("BETA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetHookEntry(ProjectScope, "alpha", ManifestEntry{
		File: "alpha.py", Rule: "a reworded rule", Source: SourceManual, RuleHash: HashRule("a reworded rule"),
	}); err != nil {
		t.Fatal(err)
	}

	c := CompareMasterSources(dir, master)
	if !c.Complete() || c.Agrees() {
		t.Fatalf("a master stamped with the old rule agreed: %+v", c)
	}
	if got := c.Drifted(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("drifted = %v, want only alpha", got)
	}
	g := c.Guards[0]
	if g.State != SourceStampsDiffer || strings.Join(g.Stamps, ",") != "rule,rule_hash" {
		t.Fatalf("alpha reads as %s %v, want stamps-differ on rule,rule_hash", g.State, g.Stamps)
	}
	if !c.StampsOnly() {
		t.Fatal("a stamp-only disagreement did not read as one")
	}
	if embeds, err := MasterEmbedsGuard(ProjectScope, "alpha"); err != nil || !embeds {
		t.Fatalf("the master's unchanged source read as not embedded: %v %v", embeds, err)
	}
	err = MasterCovers(ProjectScope)
	if err == nil || !strings.Contains(err.Error(), "alpha carries an old rule, rule_hash") {
		t.Fatalf("sync's refusal passed or misnamed the stale stamps: %v", err)
	}

	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if c := CompareMasterSources(dir, master); !c.Agrees() {
		t.Fatalf("a rebuild did not restore agreement: %+v", c)
	}
}

func TestAStampTheMasterDoesNotCarryIsUnknownNotStale(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	// A master built before rule_hash and source_id were stamped carries neither.
	text := string(data)
	object, ok := pythonDictConst(text, masterMetaConst)
	if !ok {
		t.Fatal("the generated master declares no stamps")
	}
	var meta map[string]map[string]string
	if err := json.Unmarshal([]byte(object), &meta); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"rule_hash", "source_id"} {
		if _, ok := meta["alpha"][field]; !ok {
			t.Fatalf("the generated master stamps no %s", field)
		}
		delete(meta["alpha"], field)
	}
	text = strings.Replace(text, object, string(pythonDict(meta)), 1)
	if err := os.WriteFile(master, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	if c := CompareMasterSources(dir, master); !c.Agrees() {
		t.Fatalf("a master lacking a stamp read as stale: %+v", c)
	}

	// A stamp it does carry is still compared.
	if err := SetHookEntry(ProjectScope, "alpha", ManifestEntry{File: "alpha.py", Rule: "reworded", Source: SourceManual}); err != nil {
		t.Fatal(err)
	}
	c := CompareMasterSources(dir, master)
	if len(c.Guards) != 1 || strings.Join(c.Guards[0].Stamps, ",") != "rule" {
		t.Fatalf("the carried rule stamp was not compared: %+v", c)
	}
}

func TestAStampTheManifestNoLongerBacksIsStale(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Hooks["alpha"]
	entry.RuleHash = ""
	manifest.Hooks["alpha"] = entry
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	c := CompareMasterSources(dir, master)
	if len(c.Guards) != 1 || strings.Join(c.Guards[0].Stamps, ",") != "rule_hash" {
		t.Fatalf("a rule_hash the manifest dropped was not named: %+v", c)
	}
}

func TestAnUnownedGuardFileAgreesWithItsEmptyStamps(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	if err := os.WriteFile(filepath.Join(dir, "stray.py"), []byte(blockingHook("STRAY")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ConsolidateHooks(ProjectScope); err != nil {
		t.Fatal(err)
	}
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if c := CompareMasterSources(dir, master); !c.Agrees() {
		t.Fatalf("a master built over an unowned file disagreed with it: %+v", c)
	}

	// An entry claiming the file afterwards changes the owner the master stamped.
	if err := SetHookEntry(ProjectScope, "claimed", ManifestEntry{File: "stray.py", Rule: "claim", Source: SourceManual}); err != nil {
		t.Fatal(err)
	}
	c := CompareMasterSources(dir, master)
	var stray GuardSource
	for _, g := range c.Guards {
		if g.Name == "stray" {
			stray = g
		}
	}
	if stray.State != SourceStampsDiffer || strings.Join(stray.Stamps, ",") != "hook_name,rule,source" {
		t.Fatalf("a newly owned file reads as %+v", stray)
	}
}

func TestStampsTheReaderCannotDecodeAreUnreadableNotAbsent(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	master, err := GetMasterHookPath(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := pythonDictConst(string(data), masterMetaConst)
	if !ok {
		t.Fatal("the generated master declares no stamps")
	}
	text := strings.Replace(string(data), "STEER_HOOK_META = "+object, `STEER_HOOK_META = dict(alpha={"rule": "old"})`, 1)
	if err := os.WriteFile(master, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	if c := CompareMasterSources(dir, master); c.State != ComparisonUnavailable {
		t.Fatalf("undecodable stamps read as %s", c.State)
	}
}
