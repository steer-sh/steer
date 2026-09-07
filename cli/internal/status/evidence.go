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
// Each pass below scans BOTH windows, so a permanent cause anywhere outranks a
// transient one anywhere.
func EvidenceGap(obs mine.Observation, scope *cadence.Scope) *Gap {
	if obs.Recurrence.Occurred {
		return nil
	}
	if scope == nil {
		return &Gap{Sentence: "no rule was saved for which work counts", Command: "steer fix scope"}
	}
	windows := []map[string]any{obs.Coverage.Before, obs.Coverage.After}
	// The repairable cause runs first: of the permanent ones, only this carries a
	// command. `pending` is counted inside `unknown_evidence` (mine/scope.go:265),
	// so the ledgers the resolver closed as gone are the excess over it.
	for _, c := range windows {
		if numInt(c["unknown_evidence"]) > numInt(c["pending"]) {
			return &Gap{Sentence: "review sequences inside the observation period carry no recorded decision, so they cannot be counted", Command: BackfillCommand}
		}
	}
	for _, c := range windows {
		if numInt(c["unknown_scope"]) > 0 {
			return &Gap{Sentence: "review sequences inside the observation period record no repository or changed files, so no rule can say whether they count"}
		}
		// A chain map's `unknown` is its two named causes summed, so only the
		// remainder is a cause of its own — which is the whole of a session map's
		// `unknown`, since that corpus counts one bucket and writes neither name.
		if numInt(c["unknown"])-numInt(c["unknown_scope"])-numInt(c["unknown_evidence"]) > 0 {
			return &Gap{Sentence: "agent sessions inside the observation period record no project, so no rule can say whether they count"}
		}
	}
	if obs.Firing.Applicable && !obs.Firing.LogPresent {
		return &Gap{Sentence: "this machine keeps no guard audit log, so no block can be joined to the observation"}
	}
	for _, c := range windows {
		if numInt(c["pending"]) > 0 {
			return &Gap{Sentence: "review sequences inside the observation period still await a decision, and settle on their own"}
		}
	}
	return nil
}

func GapSentence(g *Gap, obs mine.Observation, scope *cadence.Scope) string {
	if g != nil {
		return g.Sentence
	}
	return ActionSentence(obs, scope)
}
