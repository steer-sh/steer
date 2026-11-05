package checks

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RepoRootOf returns the top level of the Git work tree containing dir.
func RepoRootOf(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %s", dir, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckState is one declared check with the two facts a reader needs beside it:
// how its latest receipt relates to the check as it is now, and the last time
// this machine recorded it running. Execution is nil when no run recorded it,
// which is unobserved and never a pass.
type CheckState struct {
	Declaration Declaration
	Freshness   Freshness
	Execution   *Execution
}

// RepositoryState is one repository's check tier on this machine: what it
// declares, whether this machine authorized automatic runs, and the newest
// oracle run that did any work. Refused is a later run the repository lock
// turned away, kept beside Latest rather than in front of it.
type RepositoryState struct {
	Repo     string
	Manifest bool
	Adopted  bool
	Adoption Adoption
	Checks   []CheckState
	Latest   *OracleRun
	Refused  *OracleRun
	// StopObserved is the finish time of the newest run each harness's Stop
	// runner recorded here. A harness that never ran one has no entry, which is
	// unobserved rather than broken.
	StopObserved map[string]time.Time
}

// stopObserved reads the newest run each harness's Stop runner recorded. A run
// from another source belongs to no runner, and a refused run recorded no work.
func stopObserved(runs []OracleRun) map[string]time.Time {
	out := map[string]time.Time{}
	for _, r := range runs {
		if r.Source != SourceStop || r.Harness == "" || r.ConcurrentWith != "" {
			continue
		}
		if seen, ok := out[r.Harness]; !ok || r.Finished.After(seen) {
			out[r.Harness] = r.Finished
		}
	}
	return out
}

// LatestRuns splits a repository's run history, oldest first, into the newest
// run that executed and a refusal recorded after it. A refused run runs no
// check and reports no outcome of its own, so reporting it as the latest result
// hides the result of the run that held the repository.
func LatestRuns(runs []OracleRun) (executed *OracleRun, refused *OracleRun) {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].ConcurrentWith != "" {
			if refused == nil {
				refused = &runs[i]
			}
			continue
		}
		return &runs[i], refused
	}
	return nil, refused
}

// StateOf reads the repository's manifest, adoption, validation history, live
// executions and oracle runs. Every store is machine-local: an absent one reads
// as nothing recorded here, never as an error. A repository with no manifest
// still reports its adoption, because a repository is adoptable before it
// declares a check.
func StateOf(root string) (RepositoryState, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return RepositoryState{}, err
	}
	st := RepositoryState{Repo: identity, StopObserved: map[string]time.Time{}}
	st.Adoption, st.Adopted, err = AdoptionOf(identity)
	if err != nil {
		return st, err
	}
	if _, err := os.Stat(ManifestPath(root)); err == nil {
		st.Manifest = true
	}
	runs, err := OracleRuns(root)
	if err != nil {
		return st, err
	}
	st.Latest, st.Refused = LatestRuns(runs)
	st.StopObserved = stopObserved(runs)
	if !st.Manifest {
		return st, nil
	}
	m, err := Load(root)
	if err != nil {
		return st, err
	}
	history, err := History(root)
	if err != nil {
		return st, err
	}
	latest, err := LatestExecutions(root)
	if err != nil {
		return st, err
	}
	for _, d := range m.Checks {
		cs := CheckState{Declaration: d, Freshness: FreshnessOf(root, d, history)}
		if e, ok := latest[d.ID]; ok {
			cs.Execution = &e
		}
		st.Checks = append(st.Checks, cs)
	}
	return st, nil
}
