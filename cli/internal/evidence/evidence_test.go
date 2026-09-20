package evidence

import (
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// A failed execution is Blocked only when its own oracle run blocked the turn.
func TestAFailedExecutionIsBlockedOnlyUnderABlockingRun(t *testing.T) {
	at := time.Unix(3000, 0)
	live := checks.LiveEvidence{
		Executions: []checks.Execution{
			{RunID: "r-block", CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
			{RunID: "r-block", CheckID: "b", Outcome: checks.OutcomeNotExecuted, Started: at},
			{RunID: "r-yield", CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
			{CheckID: "a", Outcome: checks.OutcomeFailed, Started: at},
		},
		Runs: []checks.OracleRun{
			{RunID: "r-block", Disposition: checks.DispositionBlock},
			{RunID: "r-yield", Disposition: checks.DispositionYield},
		},
	}
	runs := checkRunsOf(live)
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4", len(runs))
	}
	want := []bool{true, false, false, false}
	for i, r := range runs {
		if r.Blocked != want[i] {
			t.Errorf("run %d (%s under %q) blocked = %t, want %t", i, r.Outcome, live.Executions[i].RunID, r.Blocked, want[i])
		}
	}
	if runs[0].TS != 3000 {
		t.Errorf("TS = %v, want the start time in unix seconds", runs[0].TS)
	}
}

func binding(tested bool) *claude.FixtureBinding {
	return &claude.FixtureBinding{
		Digest: "f", Cases: 11, Tested: tested, HookDigest: "h",
		MasterDigest: "m", RuleHash: "r", VerifiedAt: "2026-09-18T00:00:00Z", Shadowed: 2,
	}
}

// A manifest keeps the digests of a run whose outcome it no longer holds, so a
// cleared pass reads as no retained pass and never as a comparison.
func TestAClearedPassReadsAsNone(t *testing.T) {
	got := receiptOf(&claude.FixtureSnapshot{Binding: binding(false)})
	if got.State != mine.FixtureNone {
		t.Errorf("state = %q, want %q", got.State, mine.FixtureNone)
	}
	if got.Cases != nil || got.Shadowed != nil || got.VerifiedAt != "" {
		t.Errorf("a state with no retained pass carried its metadata: %+v", got)
	}
}

// A component this machine could not read is reported beside the ones that moved,
// with the reason, so a token never implies that changed bytes were observed.
func TestAnUnreadableComponentIsStaleWithItsReason(t *testing.T) {
	got := receiptOf(&claude.FixtureSnapshot{
		Binding:     binding(true),
		Changed:     []string{claude.FixtureComponentHook},
		Unavailable: []string{claude.FixtureComponentFixtures},
		Reason:      "the stored fixtures are no longer in the store",
	})
	if got.State != mine.FixtureStale {
		t.Fatalf("state = %q, want %q", got.State, mine.FixtureStale)
	}
	if len(got.Changed) != 2 || got.Changed[0] != claude.FixtureComponentHook ||
		got.Changed[1] != claude.FixtureComponentFixtures {
		t.Errorf("changed = %v, want the moved component then the unreadable one", got.Changed)
	}
	if got.Reason == "" {
		t.Error("a stale reading with an unreadable component carries no reason")
	}
	if got.Cases == nil || *got.Cases != 11 || got.Shadowed == nil || *got.Shadowed != 2 {
		t.Errorf("a stale reading dropped the retained pass's metadata: %+v", got)
	}
}

// A pass that named no master validates the guard under no master, so it cannot be
// read as current however well the other three components match.
func TestAPassWithNoMasterIdentityIsUnavailable(t *testing.T) {
	got := receiptOf(&claude.FixtureSnapshot{Binding: binding(true), NoMasterIdentity: true})
	if got.State != mine.FixtureUnavailable {
		t.Errorf("state = %q, want %q", got.State, mine.FixtureUnavailable)
	}
	if got.Reason == "" {
		t.Error("the missing master identity is unexplained")
	}
}

func TestAMatchingSnapshotIsCurrent(t *testing.T) {
	got := receiptOf(&claude.FixtureSnapshot{Binding: binding(true)})
	if got.State != mine.FixtureCurrent || len(got.Changed) != 0 {
		t.Errorf("a matching snapshot read %+v, want current with nothing changed", got)
	}
}

// Gather resolves a store only for a key some record can be attributed to here: a
// project store read for a record frozen against another checkout would answer
// with a different guard's receipt.
func TestGatherResolvesNoStoreForAnUnattributableRecord(t *testing.T) {
	records := []mine.Record{
		{"tier": "guard", mine.HookIDKey: "alpha", mine.HookScopeKey: "project",
			mine.HookRepoKey: "/w/other", mine.HookHostKey: "here"},
		{"tier": "guard", mine.HookIDKey: "beta", mine.HookScopeKey: "global",
			mine.HookHostKey: "elsewhere"},
		{"tier": "guard", mine.HookIDKey: "gamma"},
	}
	if got := FixtureReceipts(records, "here", "/w/steer"); len(got) != 0 {
		t.Errorf("Gather read a store for a record it cannot attribute: %+v", got)
	}
}
