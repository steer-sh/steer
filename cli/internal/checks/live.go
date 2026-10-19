package checks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/statestore"
)

// LiveEvidence is every repository's live check evidence on this machine.
type LiveEvidence struct {
	Executions []Execution
	Runs       []OracleRun
	// Repos holds each repository identity that has any check record here: a
	// validation, an execution or an oracle run.
	Repos map[string]bool
}

// ReadLiveEvidence reads the execution and oracle-run streams of every
// repository in the live check state. Isolated adoption evidence is stored
// elsewhere and never appears here. A missing state directory is empty
// evidence, not an error.
func ReadLiveEvidence() (LiveEvidence, error) {
	return readLiveEvidenceIn(statestore.Resolve(StateComponent))
}

func readLiveEvidenceIn(dir string) (LiveEvidence, error) {
	ev := LiveEvidence{Repos: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return ev, nil
	}
	if err != nil {
		return ev, err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoDir := filepath.Join(dir, entry.Name())
		errs = append(errs, readJSONL(filepath.Join(repoDir, executionsName), func(line []byte) {
			var e Execution
			if json.Unmarshal(line, &e) == nil && e.CheckID != "" {
				ev.Executions = append(ev.Executions, e)
				ev.markRepo(e.Repo)
			}
		}))
		errs = append(errs, readJSONL(filepath.Join(repoDir, runsName), func(line []byte) {
			var r OracleRun
			if json.Unmarshal(line, &r) == nil && r.RunID != "" {
				ev.Runs = append(ev.Runs, r)
				ev.markRepo(r.Repo)
			}
		}))
		errs = append(errs, readJSONL(filepath.Join(repoDir, validationsName), func(line []byte) {
			var v ValidationRun
			if json.Unmarshal(line, &v) == nil && v.CheckID != "" {
				ev.markRepo(v.Repo)
			}
		}))
	}
	return ev, errors.Join(errs...)
}

func (ev *LiveEvidence) markRepo(repo string) {
	if repo != "" {
		ev.Repos[repo] = true
	}
}
