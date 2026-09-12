package checks

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/version"
)

// StateComponent is the evidence component holding check evidence.
const StateComponent = "checks"

const validationsName = "validations.jsonl"

// RunVersion is the validation-run record format.
const RunVersion = 1

// ValidationRun is one replay of a check's cases. A receipt is a validated run
// whose detector, cases and executor version still match the current ones.
// Failed and unavailable runs are kept as history and never count as receipts.
type ValidationRun struct {
	Version         int          `json:"version"`
	RunID           string       `json:"run_id"`
	CheckID         string       `json:"check_id"`
	Kind            string       `json:"kind"`
	Repo            string       `json:"repo"`
	ExecutorVersion int          `json:"executor_version"`
	SteerVersion    string       `json:"steer_version"`
	Detector        Detector     `json:"detector"`
	CasesDigest     string       `json:"cases_digest"`
	Started         time.Time    `json:"started"`
	Finished        time.Time    `json:"finished"`
	Verdict         string       `json:"verdict"`
	Reason          string       `json:"reason,omitempty"`
	Cases           []CaseResult `json:"cases,omitempty"`
}

// RepoIdentity resolves root to the absolute, symlink-free path that names the
// repository in evidence.
func RepoIdentity(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func repoKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:8])
}

func validationsPath(dir, identity string) string {
	return filepath.Join(dir, repoKey(identity), validationsName)
}

// Validate replays one check's cases against its current detector and appends
// the run to the repository's validation history. It first pins each snapshot
// commit the cases name under SnapshotRefPrefix. It returns the run whatever
// its verdict; err reports only a run that could not be recorded.
func Validate(root string, d Declaration) (ValidationRun, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return ValidationRun{}, err
	}
	run := ValidationRun{
		Version:         RunVersion,
		RunID:           newRunID(),
		CheckID:         d.ID,
		Kind:            d.Kind,
		Repo:            identity,
		ExecutorVersion: ExecutorVersion,
		SteerVersion:    version.Version,
		Started:         time.Now().UTC(),
	}
	cases, digest, err := LoadCases(root, d.ID)
	if err != nil {
		run.Verdict, run.Reason = VerdictUnavailable, err.Error()
	} else if err := retainSnapshots(root, cases); err != nil {
		run.CasesDigest = digest
		run.Verdict, run.Reason = VerdictUnavailable, err.Error()
	} else {
		run.CasesDigest = digest
		det, results, err := Replay(root, d, cases)
		run.Detector, run.Cases = det, results
		if err != nil {
			run.Verdict, run.Reason = VerdictUnavailable, err.Error()
		} else {
			run.Verdict = verdictOf(results)
		}
	}
	run.Finished = time.Now().UTC()
	return run, appendRun(identity, run)
}

func verdictOf(results []CaseResult) string {
	verdict := VerdictValidated
	for _, r := range results {
		switch r.Outcome {
		case OutcomeMismatched:
			return VerdictFailed
		case OutcomeInvalid:
			verdict = VerdictUnavailable
		}
	}
	return verdict
}

func appendRun(identity string, run ValidationRun) error {
	dir, err := statestore.Prepare(StateComponent)
	if err != nil {
		return err
	}
	path := validationsPath(dir, identity)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("record validation: %w", err)
	}
	line, err := json.Marshal(run)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("record validation: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("record validation: %w", err)
	}
	return f.Close()
}

// History returns the repository's validation runs, oldest first. A line that
// does not parse is skipped, so one torn append cannot hide the rest.
func History(root string) ([]ValidationRun, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(validationsPath(statestore.Resolve(StateComponent), identity))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []ValidationRun
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		var run ValidationRun
		if json.Unmarshal(scanner.Bytes(), &run) == nil && run.CheckID != "" {
			runs = append(runs, run)
		}
	}
	return runs, scanner.Err()
}

// Freshness states.
const (
	// FreshCurrent is a check whose latest receipt matches its current
	// detector, cases and executor version.
	FreshCurrent = "current"
	// FreshStale is a check with a receipt whose bindings changed since.
	FreshStale = "stale"
	// FreshNone is a check that has never validated.
	FreshNone = "none"
)

// Freshness is how a check's latest receipt relates to the check as it is now.
// Stale is informational: a stale check keeps running. Changed names what moved
// since the receipt: "detector", "cases" or "executor".
type Freshness struct {
	State   string         `json:"state"`
	Receipt *ValidationRun `json:"receipt,omitempty"`
	Latest  *ValidationRun `json:"latest,omitempty"`
	Changed []string       `json:"changed,omitempty"`
	Reason  string         `json:"reason,omitempty"`
}

// FreshnessOf compares a check's latest receipt with its current detector and
// cases. Latest is the most recent run of any verdict.
func FreshnessOf(root string, d Declaration, history []ValidationRun) Freshness {
	var f Freshness
	for i := len(history) - 1; i >= 0; i-- {
		run := history[i]
		if run.CheckID != d.ID {
			continue
		}
		if f.Latest == nil {
			f.Latest = &history[i]
		}
		if run.Verdict == VerdictValidated {
			f.Receipt = &history[i]
			break
		}
	}
	if f.Receipt == nil {
		f.State = FreshNone
		return f
	}
	f.State = FreshCurrent
	if det, err := DetectorOf(root, d); err != nil {
		f.Changed, f.Reason = append(f.Changed, "detector"), err.Error()
	} else if det.Digest != f.Receipt.Detector.Digest {
		f.Changed = append(f.Changed, "detector")
	}
	if _, digest, err := LoadCases(root, d.ID); err != nil {
		f.Changed = append(f.Changed, "cases")
		if f.Reason == "" {
			f.Reason = err.Error()
		}
	} else if digest != f.Receipt.CasesDigest {
		f.Changed = append(f.Changed, "cases")
	}
	if f.Receipt.ExecutorVersion != ExecutorVersion {
		f.Changed = append(f.Changed, "executor")
	}
	if len(f.Changed) > 0 {
		f.State = FreshStale
	}
	return f
}

func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
