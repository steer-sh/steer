package status

import (
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

type Gap struct {
	Sentence string
	Command  string
}

// EvidenceGap names a missing coverage cause even when another part of the
// window has eligible work. Recurrence is handled by ActionFor before this text.
func EvidenceGap(obs mine.Observation, scope *cadence.Scope) *Gap {
	if obs.Recurrence.Occurred {
		return nil
	}
	if scope == nil {
		return &Gap{Sentence: "no rule was saved for which work counts", Command: "steer fix scope"}
	}
	for _, c := range []map[string]any{obs.Coverage.Before, obs.Coverage.After} {
		switch {
		case numInt(c["unknown_evidence"]) > 0:
			return &Gap{Sentence: "review sequences inside the observation period carry no recorded decision, so they cannot be counted", Command: BackfillCommand}
		case numInt(c["pending"]) > 0:
			return &Gap{Sentence: "review sequences inside the observation period still await a decision"}
		case numInt(c["unknown_scope"]) > 0:
			return &Gap{Sentence: "review sequences inside the observation period record no repository or changed files, so no rule can say whether they count"}
		case numInt(c["unknown"]) > 0:
			return &Gap{Sentence: "agent sessions inside the observation period record no project, so no rule can say whether they count"}
		}
	}
	if obs.Firing.Applicable && !obs.Firing.LogPresent {
		return &Gap{Sentence: "this machine keeps no guard audit log, so no block can be joined to the observation"}
	}
	return nil
}

func GapSentence(g *Gap, obs mine.Observation, scope *cadence.Scope) string {
	if g != nil {
		return g.Sentence
	}
	return ActionSentence(obs, scope)
}
