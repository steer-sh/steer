package claude

import (
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
