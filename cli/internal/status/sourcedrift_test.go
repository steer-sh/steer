package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

func drifted() MasterRuntime {
	return MasterRuntime{
		Harness: "claude-code", Scope: "project", Path: "/h/steer-hooks.py", State: MasterCurrent,
		Sources: &claude.SourceComparison{State: claude.ComparisonComplete, Guards: []claude.GuardSource{
			{Name: "alpha", State: claude.SourceMatches},
			{Name: "beta", State: claude.SourceDiffers},
		}},
	}
}

func TestADriftedSourceIsReportedApartFromRuntimeFreshness(t *testing.T) {
	m := drifted()
	// The runner carries this binary's runtime and still runs different guard
	// source. Reading one state for both facts would report this row healthy.
	if m.Stale() || m.Detached() {
		t.Fatal("the row is neither stale nor detached")
	}
	if !m.SourcesDrifted() {
		t.Fatal("an edited guard file is not reported")
	}
	if phrase := MasterSourcePhrase(m); !strings.Contains(phrase, "beta") {
		t.Fatalf("the phrase does not name the guard: %q", phrase)
	}
	key, labels, scopes := DriftedMasters([]MasterRuntime{m})
	if key == "" || len(labels) != 1 || len(scopes) != 1 {
		t.Fatalf("the nudge summary is empty: %q %v %v", key, labels, scopes)
	}
}

func TestAnUnreadComparisonNeverReportsAgreement(t *testing.T) {
	m := drifted()
	m.Sources = &claude.SourceComparison{
		State:  claude.ComparisonUnavailable,
		Reason: "failed to read hooks directory",
	}
	if m.SourcesDrifted() {
		t.Fatal("a comparison that reached no answer reported drift")
	}
	// It says so rather than falling silent, which would read as agreement.
	if phrase := MasterSourcePhrase(m); !strings.Contains(phrase, "could not be read") {
		t.Fatalf("an unavailable comparison says %q", phrase)
	}
	if key, _, _ := DriftedMasters([]MasterRuntime{m}); key != "" {
		t.Fatalf("an unread comparison nudged: %q", key)
	}
}

func TestAnUnregisteredRunnerHasNothingToCompare(t *testing.T) {
	// The comparison carries content a registered row would be shown — an
	// unavailable reading names its reason. A harness that registers no runner
	// runs no guard, so no reading of one belongs on its row.
	m := MasterRuntime{
		Harness: "codex", Scope: "global", State: MasterNotRegistered,
		Sources: &claude.SourceComparison{
			State:  claude.ComparisonUnavailable,
			Reason: "failed to read hooks directory",
			Guards: []claude.GuardSource{{Name: "beta", State: claude.SourceDiffers}},
		},
	}
	if m.SourcesDrifted() {
		t.Fatal("a harness registering no runner reported source drift")
	}
	if phrase := MasterSourcePhrase(m); phrase != "" {
		t.Fatalf("an unregistered harness is shown a source reading: %q", phrase)
	}
	if key, _, _ := DriftedMasters([]MasterRuntime{m}); key != "" {
		t.Fatalf("an unregistered harness nudged: %q", key)
	}
}

func TestADetachedRunnerIsNotAskedToRebuildForItsOwnSource(t *testing.T) {
	// The registered runner is a file the store does not own, so comparing it to
	// the store answers nothing an operator acts on: the repair is sync, and a
	// rebuild would write a different file.
	m := drifted()
	m.State = MasterDetached
	m.Store = "/w/.steer/hooks/steer-hooks.py"
	if !m.Detached() {
		t.Fatal("the row is not detached")
	}
	if m.SourcesDrifted() {
		t.Fatal("a detached row reported drift against the store")
	}
	if key, _, _ := DriftedMasters([]MasterRuntime{m}); key != "" {
		t.Fatalf("a detached row nudged a rebuild: %q", key)
	}

	// Its own store runner is the reading that bears on it, because sync refuses
	// a store its runner does not cover.
	m.StoreSources = &claude.SourceComparison{State: claude.ComparisonComplete,
		Guards: []claude.GuardSource{{Name: "beta", State: claude.SourceDiffers}}}
	if !m.StoreSourcesDrifted() {
		t.Fatal("the store runner's drift is not reported")
	}
	if phrase := MasterSourcePhrase(m); !strings.Contains(phrase, "beta") {
		t.Fatalf("the detached row says nothing about its store runner: %q", phrase)
	}
}

func TestASyncThatWouldBeRefusedIsNotTheRepairOffered(t *testing.T) {
	detached := drifted()
	detached.State = MasterDetached
	detached.Scope = "project"
	detached.StoreSources = &claude.SourceComparison{State: claude.ComparisonComplete,
		Guards: []claude.GuardSource{{Name: "beta", State: claude.SourceDiffers}}}
	rows := []MasterRuntime{detached}
	// Sync refuses a store whose runner does not carry its guards, so naming it
	// would send the reader into that refusal.
	if got := DetachedRepair(rows, "project"); got != RebuildCommand("project") {
		t.Fatalf("the repair offered is %q", got)
	}
	detached.StoreSources = &claude.SourceComparison{State: claude.ComparisonComplete,
		Guards: []claude.GuardSource{{Name: "beta", State: claude.SourceMatches}}}
	if got := DetachedRepair([]MasterRuntime{detached}, "project"); got != SyncCommand("project") {
		t.Fatalf("a store its runner covers is repaired with %q", got)
	}
}
