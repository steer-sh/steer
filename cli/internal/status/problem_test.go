package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// A declined record installed nothing, so it names the fix it considered rather
// than one that runs.
func TestFixLabelSeparatesADeclinedRecord(t *testing.T) {
	if got := FixLabel(ProblemView{}); got != "Fix" {
		t.Errorf("recorded fix = %q, want Fix", got)
	}
	if got := FixLabel(ProblemView{Declined: true}); got != "Declined" {
		t.Errorf("declined fix = %q, want Declined", got)
	}
}

// The fix line names the type first and the path with it, so a reader knows what
// kind of fix this is before reading what it does.
func TestFixSummaryNamesTheTypeThenTheFix(t *testing.T) {
	got := FixSummary(ProblemView{Tier: "check", Target: "check.sh",
		Intervention: "check.sh refuses a literal count"})
	if !strings.HasPrefix(got, "repository check → check.sh — ") {
		t.Errorf("fix summary = %q", got)
	}
	if got := FixSummary(ProblemView{Tier: "check", Target: "check.sh"}); got != "repository check → check.sh" {
		t.Errorf("fix summary with no prose = %q", got)
	}
}

// Missing evidence carries a repair step, while an observed recurrence carries
// the explanation that warrants inspection.
func TestProblemWhyAndNextFollowObservation(t *testing.T) {
	v := ProblemView{Scope: &cadence.Scope{Version: 1, All: true}, Unit: "chain",
		Observation: mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"pending": 4}}}}
	why, next := ProblemWhy(v), ProblemNext(v)
	if next == "" {
		t.Fatal("a result that asks for work carries a step")
	}
	if why != "" {
		t.Errorf("both a reason %q and a step %q", why, next)
	}
	recurrence := ProblemView{Scope: &cadence.Scope{Version: 1, All: true},
		Observation: mine.Observation{Recurrence: mine.Recurrence{Occurred: true}}}
	if ProblemNext(recurrence) == "" || ProblemWhy(recurrence) != "" {
		t.Errorf("a recurrence asks the reader to look at something, so it is a step: why=%q next=%q",
			ProblemWhy(recurrence), ProblemNext(recurrence))
	}
	// A quiet window asks nothing, so its sentence is a reason and never a step.
	quiet := ProblemView{Scope: &cadence.Scope{Version: 1, All: true},
		Observation: mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": 9}}}}
	if ProblemWhy(quiet) == "" || ProblemNext(quiet) != "" {
		t.Errorf("a quiet window carries a reason and no step: why=%q next=%q",
			ProblemWhy(quiet), ProblemNext(quiet))
	}
}

// A queued row says where its evidence came from in the reader's words. The
// stored name arrives as data, so no source file carries it.
func TestCandidateEvidenceTranslatesTheStoredSource(t *testing.T) {
	got := CandidateEvidence(cadence.Candidate{Corpus: "review-findings",
		Pattern: "missing scope", Count: 1, Sessions: 1})
	for _, want := range []string{"a repeated review finding", "1 recurrence", "1 agent session"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "review-findings") {
		t.Errorf("evidence prints the stored name: %q", got)
	}
}
