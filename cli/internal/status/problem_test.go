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

// A hard comparison limit IS the reason a result is withheld, and it names the
// changed conditions the generic sentence cannot, so it replaces that sentence
// rather than printing beside it.
func TestProblemWhyPrefersTheComparisonLimit(t *testing.T) {
	v := ProblemView{
		Verdict:   "confounded",
		Unit:      "chain",
		Confounds: []mine.Confound{{Category: "instrument"}},
	}
	why := ProblemWhy(v)
	limit := ComparisonLimit("confound", v.Confounds)
	if limit == "" || why != limit {
		t.Errorf("why = %q, want the comparison limit %q", why, limit)
	}
	if next := ProblemNext(v); next != "" {
		t.Errorf("a withheld result asks for no step, got %q", next)
	}
}

// A result that asks for work carries a step and no reason, so a reader is never
// given a sentence under both labels.
func TestProblemWhyAndNextAreExclusive(t *testing.T) {
	v := ProblemView{
		Verdict:        "evidence-incomplete",
		Unit:           "chain",
		BeforeCoverage: cadence.Coverage{Total: 10, Pending: 4},
	}
	why, next := ProblemWhy(v), ProblemNext(v)
	if next == "" {
		t.Fatal("a result that asks for work carries a step")
	}
	if why != "" {
		t.Errorf("both a reason %q and a step %q", why, next)
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
