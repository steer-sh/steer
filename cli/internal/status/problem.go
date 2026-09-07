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
	Declined     bool
	Tier         string
	Target       string
	Intervention string
	Verdict      string
	Unit         string
	// Unmeasurable is this record's reading of the window it still needs,
	// computed once by the caller so every field agrees.
	Unmeasurable   bool
	BeforeCoverage cadence.Coverage
	AfterCoverage  cadence.Coverage
	Attribution    cadence.Attribution
	Confounds      []mine.Confound
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
	return VerdictLabel(v.Verdict, v.Unmeasurable)
}

// ProblemWhy is the reason a result is unavailable, and is empty where the reader
// has a step instead. A hard comparison limit IS that reason, and it names the
// changed conditions the generic sentence cannot, so it replaces rather than
// joins it.
func ProblemWhy(v ProblemView) string {
	if limit := ComparisonLimit("confound", v.Confounds); limit != "" {
		return limit
	}
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
// result is unavailable and the step the reader can take. A result the scorer
// withheld carries a reason and no step; a result that asks for work carries a
// step. An unreachable window is the exception: its sentence names two choices,
// so it is a step even though the entry asks for no repair.
func problemReason(v ProblemView) (why, next string) {
	gap := EvidenceGap(v.Verdict, v.Unit, v.BeforeCoverage, v.AfterCoverage, v.Attribution)
	sentence := GapSentence(gap, v.Verdict, v.Unmeasurable)
	if sentence == "" {
		return "", ""
	}
	if gap != nil && gap.Command != "" {
		sentence += " — run: " + gap.Command
	}
	if !v.Unmeasurable {
		switch ActionFor(v.Verdict, false) {
		case ActionAccept, ActionSettled:
			return sentence, ""
		}
	}
	return "", sentence
}

// CandidateEvidence is a queued row's own counts, in the source's words.
func CandidateEvidence(c cadence.Candidate) string {
	if c.Pattern != "" {
		return fmt.Sprintf("%s in your session history: %s in %d agent %s",
			signalSource(c.Corpus), Recurrences(c.Count), c.Sessions,
			plural("session", c.Sessions))
	}
	return fmt.Sprintf("%d %s carry a finding no problem covers yet",
		c.UndecidedChains, UnitName("chain", c.UndecidedChains))
}

// CandidateNext is the handoff every queued row shares. Nothing in the terminal
// or the dashboard authors a record, so the text names the one thing that does.
const CandidateNext = "run /steer-report in your coding agent to read these and decide each one"

// signalSource names where a queued row's evidence came from, in the reader's
// words. The name arrives as stored data, so it is translated here.
func signalSource(corpus string) string {
	switch corpus {
	case "review-findings":
		return "a repeated review finding"
	}
	return "a repeated pattern"
}

// Recurrences counts occurrences with its own noun, so no bare number reaches a
// line without saying what it counts.
func Recurrences(n int) string {
	return fmt.Sprintf("%d %s", n, plural("recurrence", n))
}
