package status

import (
	"fmt"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// A recorded problem reads the same way in the terminal and in the dashboard: a
// title, then what the fix is, what the result is, and one reason or one step.
// The phrasing lives here so the two surfaces cannot drift, and each surface
// keeps its own colours, its own column widths and its own order.

// ProblemView is one recorded problem reduced to the fields a summary reads. The
// terminal builds it from the queue record and the dashboard from the scored
// inventory, so neither reader re-derives a sentence the other already has.
type ProblemView struct {
	// Declined is true for a fix that was considered and refused. Such a record
	// installed nothing, so what it describes is the fix it considered.
	Declined       bool
	Tier           string
	Target         string
	Intervention   string
	Observation    mine.Observation
	Scope          *cadence.Scope
	Unit           string
	BeforeCoverage cadence.Coverage
	AfterCoverage  cadence.Coverage
}

// FixLabel names the field the fix prints under.
func FixLabel(v ProblemView) string {
	if v.Declined {
		return "Declined"
	}
	return "Fix"
}

// FixSummary says what the fix does and what type it is. It never claims the fix
// is installed and running: the record carries what was decided, not evidence of
// the current setup.
func FixSummary(v ProblemView) string {
	kind := TierPhrase(v.Tier, v.Target)
	if v.Intervention == "" {
		return kind
	}
	return kind + " — " + v.Intervention
}

// ResultLabel is the measured result in the reader's words.
func ResultLabel(v ProblemView) string {
	return ObservationLabel(v.Observation)
}

// ProblemWhy is the reason a result is unavailable, and is empty where the reader
// has a step instead. A hard comparison limit IS that reason, and it names the
// changed conditions the generic sentence cannot, so it replaces rather than
// joins it.
func ProblemWhy(v ProblemView) string {
	why, _ := problemReason(v)
	return why
}

// ProblemNext is the step the reader can take, and is empty where no repair
// exists.
func ProblemNext(v ProblemView) string {
	_, next := problemReason(v)
	return next
}

// problemReason splits the entry's one explanatory sentence into the reason a
// result is unavailable and the step the reader can take. An observation that
// asks nothing carries a reason and no step; one that asks the reader to look at
// something carries a step.
func problemReason(v ProblemView) (why, next string) {
	gap := EvidenceGap(v.Observation, v.Scope)
	sentence := GapSentence(gap, v.Observation, v.Scope)
	if sentence == "" {
		return "", ""
	}
	if gap != nil && gap.Command != "" {
		sentence += " — run: " + gap.Command
	}
	if ActionFor(v.Observation, v.Scope) == ActionObserve {
		return sentence, ""
	}
	return "", sentence
}

// HeadingUngrouped heads the queued rows on both surfaces, under the tracked
// problems rather than above them. A queued row is a pile of evidence, so the
// heading names what the pile is instead of calling its contents problems.
const HeadingUngrouped = "Evidence not yet grouped into problems"

// CandidateTitle names one queued row. The title comes from the corpus, never
// from an item inside the pile: an item is one phrasing or one finding, and
// printing it as a title claims a grouping nothing has made yet.
func CandidateTitle(c cadence.Candidate) string {
	if c.Corpus == cadence.CorpusTranscript {
		return "Correction snippets not yet grouped into a problem"
	}
	return "Review findings not yet grouped into a problem"
}

// CandidateEvidence is a queued row's own count, in the source's words. It says
// how big the pile is and never calls the count a recurrence: a transcript row
// counts phrasings of possibly unrelated complaints, and a review row counts
// chains no class has decided.
func CandidateEvidence(c cadence.Candidate) string {
	if c.Corpus == cadence.CorpusTranscript {
		return fmt.Sprintf("%d %s in your session history that no problem covers yet",
			c.UngroupedClusters, plural("correction snippet", c.UngroupedClusters))
	}
	return fmt.Sprintf("%d %s carry a finding no problem covers yet",
		c.UndecidedChains, UnitName("chain", c.UndecidedChains))
}

// CandidateNext is the handoff every queued row shares. Nothing in the terminal
// or the dashboard authors a record, so the text names the one thing that does.
const CandidateNext = "run /steer-report in your coding agent to read these and decide each one"
