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
	// `pending` is counted inside `unknown_evidence`, so a window the resolver can
	// repair is the excess over it.
	v := ProblemView{Scope: &cadence.Scope{Version: 1, All: true}, Unit: "chain",
		Observation: mine.Observation{Coverage: mine.ObservationCoverage{
			After: map[string]any{"unknown": 6, "unknown_evidence": 6, "pending": 4}}}}
	why, next := ProblemWhy(v), ProblemNext(v)
	if next == "" {
		t.Fatal("a result that asks for work carries a step")
	}
	if why != "" {
		t.Errorf("both a reason %q and a step %q", why, next)
	}
	// A window whose unknowns are all still settling asks for nothing: those rows
	// close on their own, so the sentence is a reason and never a step.
	settling := ProblemView{Scope: &cadence.Scope{Version: 1, All: true}, Unit: "chain",
		Observation: mine.Observation{Coverage: mine.ObservationCoverage{
			After: map[string]any{"unknown": 4, "unknown_evidence": 4, "pending": 4}}}}
	if ProblemWhy(settling) == "" || ProblemNext(settling) != "" {
		t.Errorf("a settling window carries a reason and no step: why=%q next=%q",
			ProblemWhy(settling), ProblemNext(settling))
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

// A queued row says how big its pile is in the reader's words. The stored corpus
// name arrives as data, so no source file carries it.
func TestCandidateEvidenceTranslatesTheStoredSource(t *testing.T) {
	got := CandidateEvidence(cadence.Candidate{Corpus: "review-findings", UndecidedChains: 1})
	for _, want := range []string{"1 review sequence", "no problem covers yet"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "review-findings") {
		t.Errorf("evidence prints the stored name: %q", got)
	}
}

// A transcript row counts phrasings, so its sentence may never say recurrence: a
// cluster key is the user's own wording, and one problem complained about five
// ways is five keys.
func TestTranscriptCandidateEvidenceClaimsNoRecurrence(t *testing.T) {
	got := CandidateEvidence(cadence.Candidate{Corpus: cadence.CorpusTranscript, UngroupedClusters: 25})
	if !strings.Contains(got, "25 correction snippets") {
		t.Errorf("evidence = %q, missing the pile size", got)
	}
	for _, banned := range []string{"recurrence", "repeated", "agent session"} {
		if strings.Contains(got, banned) {
			t.Errorf("evidence = %q, still claims %q", got, banned)
		}
	}
}

// The title comes from the corpus, never from one item of the pile. Printing an
// item as the title is what made a single user sentence read as a problem.
func TestCandidateTitleNamesThePileNotItsContents(t *testing.T) {
	got := CandidateTitle(cadence.Candidate{Corpus: cadence.CorpusTranscript,
		UngroupedClusters: 3, Examples: []string{"no i don t think we need reviewer drift"}})
	if got != "Correction snippets not yet grouped into a problem" {
		t.Errorf("title = %q", got)
	}
}
