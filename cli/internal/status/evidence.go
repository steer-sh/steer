// The four causes of a withheld evidence-incomplete result, and the one of them a
// command repairs. The token stays one token: the coverage counts that separate
// the causes already reach every renderer, so the sentence is chosen from them.

package status

import "github.com/steer-sh/steer-cli/internal/cadence"

// Gap is why a window withheld for missing evidence, plus the command that
// clears it. Command is empty for the three causes no command repairs, and a
// reader is told the cause rather than handed a command that changes nothing.
type Gap struct {
	Sentence string
	Command  string
}

// EvidenceGap names the cause behind one evidence-incomplete result. It returns
// nil for every other verdict, and nil when the counts name no cause — the
// action sentence then carries the row.
//
// The order mirrors the scorers. The chain engine reads a window that kept no
// eligible chain and asks whether the unknowns are ledgers the resolver closed as
// gone or chains that carried no scope metadata (mine.evidenceVerdict); only the
// first is repaired by the resolver. The session engine reads an empty window
// first and the absent audit stream second (mine.sessionVerdict), so this does
// too.
func EvidenceGap(verdict, unit string, before, after cadence.Coverage, attr cadence.Attribution) *Gap {
	if verdict != "evidence-incomplete" {
		return nil
	}
	for _, c := range []cadence.Coverage{before, after} {
		if c.Eligible > 0 {
			continue
		}
		// The two corpora break their unknowns down differently: the chain
		// corpus counts a cause per unknown, the session corpus counts one
		// bucket. Reading the chain fields over a session window would name a
		// cause off counts that corpus never writes.
		if orUnit(unit) == "session" {
			if c.Unknown > 0 {
				return &Gap{Sentence: "agent sessions inside the compared periods record no project, so no rule can say whether they count"}
			}
			continue
		}
		switch {
		case c.UnknownEvidence > c.Pending:
			return &Gap{
				Sentence: "review sequences inside the compared periods carry no recorded decision, so they cannot be counted",
				Command:  BackfillCommand,
			}
		case c.UnknownScope > 0:
			return &Gap{Sentence: "review sequences inside the compared periods record no repository or changed files, so no rule can say whether they count"}
		}
	}
	if attr.Applicable && !attr.LogPresent {
		return &Gap{Sentence: "this machine keeps no guard audit log, so no block can be joined to a session"}
	}
	return nil
}

// GapSentence is the instruction under one class: the cause where a cause is
// known, and the action sentence everywhere else.
func GapSentence(g *Gap, verdict string, unmeasurable bool) string {
	if g != nil {
		return g.Sentence
	}
	return ActionSentence(verdict, unmeasurable)
}
