package mine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

func guardRecord(scope, repo, host string) Record {
	return Record{
		"tier": "guard", HookIDKey: "alpha", HookScopeKey: scope,
		HookRepoKey: repo, HookHostKey: host,
	}
}

func receiptEvidence() EvidenceSet {
	return EvidenceSet{
		FixtureHost:        "here",
		FixtureProjectRoot: "/w/steer",
		FixtureReceipts: map[FixtureKey]FixtureReceipt{
			{Scope: "project", HookID: "alpha"}: {Applicable: true, State: FixtureCurrent},
			{Scope: "global", HookID: "alpha"}:  {Applicable: true, State: FixtureCurrent},
		},
	}
}

// A project store resolves from the working directory, so one record's match must
// not lend its receipt to a record frozen against another checkout.
func TestAProjectReceiptReachesOnlyTheCheckoutItWasFrozenAgainst(t *testing.T) {
	ev := receiptEvidence()
	if got := fixtureReceiptOf(guardRecord("project", "/w/steer", "here"), ev); got.State != FixtureCurrent {
		t.Errorf("the owning checkout did not reach its receipt: %+v", got)
	}
	other := fixtureReceiptOf(guardRecord("project", "/w/other", "here"), ev)
	if other.State != FixtureUnavailable {
		t.Errorf("a record from another checkout read %q, want %q", other.State, FixtureUnavailable)
	}
	if other.Reason == "" {
		t.Error("an unattributed record names no reason")
	}
}

// A record that froze no checkout cannot be attributed, and a legacy record must
// not borrow whichever store this command happens to resolve.
func TestARecordWithoutFrozenOwnershipIsUnavailable(t *testing.T) {
	ev := receiptEvidence()
	for name, record := range map[string]Record{
		"no store":    guardRecord("", "", "here"),
		"no checkout": guardRecord("project", "", "here"),
	} {
		if got := fixtureReceiptOf(record, ev); got.State != FixtureUnavailable {
			t.Errorf("%s read %q, want %q", name, got.State, FixtureUnavailable)
		}
	}
}

// The same absolute path on another machine is a different store.
func TestAReceiptDoesNotCrossMachines(t *testing.T) {
	got := fixtureReceiptOf(guardRecord("global", "", "elsewhere"), receiptEvidence())
	if got.State != FixtureUnavailable {
		t.Errorf("a record frozen on another host read %q, want %q", got.State, FixtureUnavailable)
	}
}

// A guard no store on this machine holds is unreached, not unvalidated: absence of
// a pass is a claim only a store this reading opened can support.
func TestAnUnresolvedGuardIsUnavailableAndNotNone(t *testing.T) {
	ev := receiptEvidence()
	ev.FixtureReceipts = map[FixtureKey]FixtureReceipt{}
	if got := fixtureReceiptOf(guardRecord("global", "", "here"), ev); got.State != FixtureUnavailable {
		t.Errorf("an unresolved guard read %q, want %q", got.State, FixtureUnavailable)
	}
}

// A tier that stores no fixtures prints no fixture line at all.
func TestANonGuardRecordIsNotApplicable(t *testing.T) {
	record := guardRecord("global", "", "here")
	record["tier"] = "check"
	if got := fixtureReceiptOf(record, receiptEvidence()); got.Applicable {
		t.Errorf("a check-tier record claimed a fixture receipt: %+v", got)
	}
}

// A rename leaves a record frozen under the old name; a declared former name still
// owns the store, while an undeclared one stays another machine's.
func TestAFormerHostNameKeepsItsRecordsOwnership(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("COMMIT_REVIEW_HOST", "")
	if err := os.MkdirAll(filepath.Join(home, ".steer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".steer", "host"), []byte("here\nold-name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := receiptEvidence()
	if gap := storeOwnershipGap(GuardFix{ID: "alpha", Scope: "global", Host: "old-name"}, ev); gap != "" {
		t.Errorf("a former name read as another machine: %s", gap)
	}
	if gap := storeOwnershipGap(GuardFix{ID: "alpha", Scope: "global", Host: "elsewhere"}, ev); gap == "" {
		t.Error("an undeclared name read as this machine")
	}
	fix := boundCheckRecord(2000)
	fix[CheckHostKey] = "old-name"
	if act, _, bound := checkActivityBase(fix, EvidenceSet{}); !bound || act.OtherHost {
		t.Errorf("a former name's check read as another machine: %+v", act)
	}
}
