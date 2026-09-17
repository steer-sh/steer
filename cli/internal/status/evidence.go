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
// Each pass below scans BOTH windows, so a cause that asks for the reader anywhere
// outranks a disclosure anywhere.
func EvidenceGap(obs mine.Observation, scope *cadence.Scope) *Gap {
	if obs.Recurrence.Occurred {
		return nil
	}
	if scope == nil {
		return &Gap{Sentence: "no rule was saved for which work counts", Command: "steer fix scope"}
	}
	windows := []map[string]any{obs.Coverage.Before, obs.Coverage.After}
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
	if obs.Checks.Missing() {
		return &Gap{Sentence: "this machine holds no check evidence for the repository, so no check run can be joined to the observation"}
	}
	// The ledger causes carry no command: the resolver's own status entry reads an
	// unresolved row, and a closed miss has no repair.
	for _, c := range windows {
		if numInt(c["ledger_overdue"]) > 0 {
			return &Gap{Sentence: "review sequences inside the observation period have no outcome read yet, so they are left out of the counts"}
		}
	}
	for _, c := range windows {
		if numInt(c["ledger_missed"]) > 0 {
			return &Gap{Sentence: "review sequences inside the observation period have no decision in any session history read here, so they are left out of the counts"}
		}
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
