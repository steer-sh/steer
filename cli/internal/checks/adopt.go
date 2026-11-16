package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/version"
)

const adoptionsName = "adoptions.jsonl"

// AdoptionVersion is the adoption event format.
const AdoptionVersion = 1

// Adoption actions.
const (
	ActionAdopt   = "adopt"
	ActionUnadopt = "unadopt"
)

// ErrNotAdopted reports an adoption that its validation or oracle run refused.
var ErrNotAdopted = errors.New("repository not adopted")

// Adoption is one change to a repository's machine-local authorization to run
// its oracle automatically. Repo is the resolved Git root; the latest event for
// that exact path decides whether it is adopted. An adopt event names the
// isolated oracle run and the receipt of each declared check it relied on.
type Adoption struct {
	Version      int               `json:"version"`
	Action       string            `json:"action"`
	Repo         string            `json:"repo"`
	At           time.Time         `json:"at"`
	OracleRun    string            `json:"oracle_run,omitempty"`
	Receipts     map[string]string `json:"receipts,omitempty"`
	SteerVersion string            `json:"steer_version"`
}

func adoptionsPath(dir string) string { return filepath.Join(dir, adoptionsName) }

// latestAdoptionEvents returns the latest adopt or unadopt event per identity.
func latestAdoptionEvents() (map[string]Adoption, error) {
	latest := map[string]Adoption{}
	err := readJSONL(adoptionsPath(statestore.Resolve(StateComponent)), func(line []byte) {
		var a Adoption
		if json.Unmarshal(line, &a) == nil && a.Repo != "" {
			latest[a.Repo] = a
		}
	})
	return latest, err
}

// Adoptions returns the roots adopted on this machine by their own event, keyed
// by identity. A linked worktree that inherits its main checkout's adoption is
// not listed; AdoptionOf reads it.
func Adoptions() (map[string]Adoption, error) {
	latest, err := latestAdoptionEvents()
	if err != nil {
		return nil, err
	}
	for repo, a := range latest {
		if a.Action != ActionAdopt {
			delete(latest, repo)
		}
	}
	return latest, nil
}

// AdoptionOf returns the event that adopted identity, if it is adopted. An
// event for identity itself decides. A linked worktree with no event of its own
// inherits its main checkout's adoption, and the returned event names that
// checkout.
func AdoptionOf(identity string) (Adoption, bool, error) {
	latest, err := latestAdoptionEvents()
	if err != nil {
		return Adoption{}, false, err
	}
	if a, ok := latest[identity]; ok {
		return a, a.Action == ActionAdopt, nil
	}
	if main, ok := MainCheckout(identity); ok {
		if a, ok := latest[main]; ok && a.Action == ActionAdopt {
			return a, true, nil
		}
	}
	return Adoption{}, false, nil
}

// AdoptOptions configures the isolated oracle run that adoption performs.
type AdoptOptions struct {
	Timeout  time.Duration
	Stdout   io.Writer
	Stderr   io.Writer
	SteerBin string
}

// AdoptValidation is the receipt adoption relied on for one declared check:
// a current receipt reused, or a replay it ran.
type AdoptValidation struct {
	CheckID string
	Reused  bool
	Run     ValidationRun
}

// AdoptResult is what an adoption attempt found. Already is set when the
// repository was adopted before the call, which then runs nothing.
type AdoptResult struct {
	Already     bool
	Adoption    Adoption
	Validations []AdoptValidation
	Oracle      OracleRun
	Executions  []Execution
}

// Adopt authorizes automatic oracle runs for the repository. Each declared
// check needs a current receipt; a check without one is replayed. The oracle
// then runs with isolated evidence and must pass with every declared check
// reported. Only then is the adopt event appended; a refusal returns
// ErrNotAdopted with what was found.
func Adopt(root string, opts AdoptOptions) (AdoptResult, error) {
	var res AdoptResult
	identity, err := RepoIdentity(root)
	if err != nil {
		return res, err
	}
	if a, ok, err := AdoptionOf(identity); err != nil {
		return res, err
	} else if ok {
		res.Already, res.Adoption = true, a
		return res, nil
	}
	if _, err := os.Stat(OraclePath(identity)); errors.Is(err, os.ErrNotExist) {
		return res, fmt.Errorf("%w: %s: run `steer check init`", ErrNoOracle, identity)
	}
	var declared []Declaration
	if m, err := Load(identity); err == nil {
		declared = m.Checks
	} else if !errors.Is(err, ErrNoManifest) {
		return res, err
	}
	history, err := History(identity)
	if err != nil {
		return res, err
	}
	var unvalidated []string
	for _, d := range declared {
		if f := FreshnessOf(identity, d, history); f.State == FreshCurrent {
			res.Validations = append(res.Validations, AdoptValidation{CheckID: d.ID, Reused: true, Run: *f.Receipt})
			continue
		}
		run, err := Validate(identity, d)
		if err != nil {
			return res, err
		}
		res.Validations = append(res.Validations, AdoptValidation{CheckID: d.ID, Run: run})
		if run.Verdict != VerdictValidated {
			unvalidated = append(unvalidated, d.ID)
		}
	}
	if len(unvalidated) > 0 {
		return res, fmt.Errorf("%w: %s did not validate", ErrNotAdopted, strings.Join(unvalidated, ", "))
	}

	isolated, err := os.MkdirTemp("", "steer-adopt-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(isolated)
	run, records, err := RunOracle(identity, RunOptions{
		Source: SourceAdopt, Timeout: opts.Timeout, Stdout: opts.Stdout, Stderr: opts.Stderr,
		SteerBin: opts.SteerBin, EvidenceDir: isolated,
	})
	res.Oracle, res.Executions = run, records
	if err != nil {
		return res, err
	}
	switch {
	case run.IntegrationFailure:
		if run.Reason != "" {
			return res, fmt.Errorf("%w: %s", ErrNotAdopted, run.Reason)
		}
		return res, fmt.Errorf("%w: %s passed without running %s", ErrNotAdopted, OracleName, strings.Join(run.Missing, ", "))
	case !run.Passed():
		return res, fmt.Errorf("%w: %s %s", ErrNotAdopted, OracleName, run.Outcome)
	}

	a := Adoption{
		Version:      AdoptionVersion,
		Action:       ActionAdopt,
		Repo:         identity,
		At:           time.Now().UTC(),
		OracleRun:    run.RunID,
		Receipts:     map[string]string{},
		SteerVersion: version.Version,
	}
	for _, v := range res.Validations {
		a.Receipts[v.CheckID] = v.Run.RunID
	}
	if err := appendAdoption(a); err != nil {
		return res, err
	}
	res.Adoption = a
	return res, nil
}

// Unadopt withdraws the authorization for identity, a resolved root. In a
// linked worktree that inherits its main checkout's adoption, the withdrawal
// covers the worktree only. It reports false when the repository was not
// adopted. Evidence stays.
func Unadopt(identity string) (bool, error) {
	if _, ok, err := AdoptionOf(identity); err != nil || !ok {
		return false, err
	}
	return true, appendAdoption(Adoption{
		Version:      AdoptionVersion,
		Action:       ActionUnadopt,
		Repo:         identity,
		At:           time.Now().UTC(),
		SteerVersion: version.Version,
	})
}

func appendAdoption(a Adoption) error {
	dir, err := statestore.Prepare(StateComponent)
	if err != nil {
		return err
	}
	if err := appendJSONL(adoptionsPath(dir), a); err != nil {
		return fmt.Errorf("record adoption: %w", err)
	}
	return nil
}
