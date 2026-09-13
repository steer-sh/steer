// The check tier's share of the queue: the open steps in the repository the
// refresh ran in. It reads the check stores only, so no harness adapter enters
// the deterministic tier; the runner's registration reaches the skill through
// `steer status --json`.

package cadence

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/checks"
)

// The open steps a repository's check tier can report. Stale validation is not
// among them: a stale check keeps running, and the label is information rather
// than a repair the queue asks for on every refresh.
const (
	// CheckNotAdopted is a repository declaring checks that this machine never
	// authorized to run at turn end.
	CheckNotAdopted = "not-adopted"
	// CheckNeverValidated is a declared check whose cases were never replayed
	// here, so nothing establishes that it fails on the defect it names.
	CheckNeverValidated = "never-validated"
	// CheckIntegrationFailure is a passing script that ran no declared check,
	// which is the script's wiring and never a failed assertion.
	CheckIntegrationFailure = "integration-failure"
)

// CheckWork is one open step in the repository's check tier, with the command
// that clears it.
type CheckWork struct {
	Kind    string `json:"kind"`
	Repo    string `json:"repo"`
	CheckID string `json:"check_id,omitempty"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix"`
}

// checkWork reads the repository's check stores and reports its open steps. A
// caller naming no repository gets none, and an unreadable store is skipped
// rather than raised: the queue is one section of a refresh that must not fail
// because a machine holds no check evidence.
func checkWork(root string) []CheckWork {
	if root == "" {
		return []CheckWork{}
	}
	out := []CheckWork{}
	st, err := checks.StateOf(root)
	if err != nil {
		return out
	}
	if st.Manifest && !st.Adopted {
		out = append(out, CheckWork{
			Kind: CheckNotAdopted, Repo: st.Repo,
			Detail: "this machine has not authorized " + checks.OracleName + " to run at turn end here",
			Fix:    "steer check adopt",
		})
	}
	for _, cs := range st.Checks {
		if cs.Freshness.State != checks.FreshNone {
			continue
		}
		out = append(out, CheckWork{
			Kind: CheckNeverValidated, Repo: st.Repo, CheckID: cs.Declaration.ID,
			Detail: "no replay of its cases was recorded here, so nothing shows it fails on the defect it names",
			Fix:    "steer check test " + cs.Declaration.ID,
		})
	}
	if st.Latest != nil && st.Latest.IntegrationFailure {
		out = append(out, CheckWork{
			Kind: CheckIntegrationFailure, Repo: st.Repo,
			Detail: fmt.Sprintf("%s passed without running %s, so no assertion of theirs was reached; "+
				"the managed line is missing from its sequence",
				checks.OracleName, strings.Join(st.Latest.Missing, ", ")),
			Fix: "steer check init",
		})
	}
	return out
}
