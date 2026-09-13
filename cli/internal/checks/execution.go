package checks

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
)

const executionsName = "executions.jsonl"

// ExecutionVersion is the execution record format.
const ExecutionVersion = 1

// The environment a parent oracle sets for the executors it runs.
const (
	EnvRunID    = "STEER_CHECK_RUN_ID"
	EnvSource   = "STEER_CHECK_SOURCE"
	EnvHarness  = "STEER_CHECK_HARNESS"
	EnvSession  = "STEER_CHECK_SESSION"
	EnvDeadline = "STEER_CHECK_DEADLINE"
	// EnvEvidenceDir, an absolute directory, replaces the live evidence root
	// for execution and oracle-run records, so an adoption run leaves no live
	// activity.
	EnvEvidenceDir = "STEER_CHECK_EVIDENCE_DIR"
)

// SourceShell is the invocation source when no parent oracle names one.
const SourceShell = "shell"

// The executors that write execution records.
const (
	ExecutorScript = "exec"
	ExecutorGoTest = "go-test"
)

// Execution outcomes. Only OutcomePassed and OutcomePassedCached are passes.
const (
	OutcomePassed = "passed"
	// OutcomePassedCached is a Go test whose pass the toolchain replayed from
	// its cache: evidence that it passed, not that it ran now.
	OutcomePassedCached = "passed-cached"
	OutcomeFailed       = "failed"
	OutcomeSkipped      = "skipped"
	// OutcomeError is a script that could not run, or that called a command
	// that could not run.
	OutcomeError = "error"
	// OutcomeTimeout is a check the deadline stopped.
	OutcomeTimeout = "timeout"
	// OutcomeNotExecuted is a check the executor never started or never saw
	// finish before the deadline.
	OutcomeNotExecuted = "not-executed"
	// OutcomeUnavailable is a bound Go test in a package that ran without a
	// result for it: the package did not build, or the test did not report.
	OutcomeUnavailable = "unavailable"
)

// Invocation is what a parent oracle tells an executor about the run it
// belongs to. RunID associates the executor's records with that run; it is
// empty when the executor runs outside an instrumented oracle. EvidenceDir,
// when set, is the root that receives the records in place of the live one.
type Invocation struct {
	RunID       string
	Source      string
	Harness     string
	Session     string
	Deadline    time.Time
	EvidenceDir string
}

// InvocationFromEnv reads the invocation a parent oracle set. A malformed
// deadline is an error, so a bounded run never silently becomes unbounded; a
// relative evidence directory is an error, so isolated records never land
// beside the working directory.
func InvocationFromEnv() (Invocation, error) {
	inv := Invocation{
		RunID:       os.Getenv(EnvRunID),
		Source:      os.Getenv(EnvSource),
		Harness:     os.Getenv(EnvHarness),
		Session:     os.Getenv(EnvSession),
		EvidenceDir: os.Getenv(EnvEvidenceDir),
	}
	if inv.EvidenceDir != "" && !filepath.IsAbs(inv.EvidenceDir) {
		return Invocation{}, fmt.Errorf("%s=%q: the directory must be absolute", EnvEvidenceDir, inv.EvidenceDir)
	}
	if inv.Source == "" {
		inv.Source = SourceShell
	}
	if raw := os.Getenv(EnvDeadline); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Invocation{}, fmt.Errorf("%s=%q: %w", EnvDeadline, raw, err)
		}
		inv.Deadline = t
	}
	return inv, nil
}

// deadlineWithin returns the earlier of the invocation's deadline and
// start+timeout. A zero timeout adds no bound.
func (inv Invocation) deadlineWithin(start time.Time, timeout time.Duration) time.Time {
	deadline := inv.Deadline
	if timeout > 0 {
		if own := start.Add(timeout); deadline.IsZero() || own.Before(deadline) {
			deadline = own
		}
	}
	return deadline
}

// Execution is one check's result in one executor invocation. It is live
// evidence, kept apart from validation runs and from the parent oracle's own
// outcome.
type Execution struct {
	Version        int       `json:"version"`
	RunID          string    `json:"run_id,omitempty"`
	Invocation     string    `json:"invocation"`
	Repo           string    `json:"repo"`
	Source         string    `json:"source"`
	Harness        string    `json:"harness,omitempty"`
	Session        string    `json:"session,omitempty"`
	Executor       string    `json:"executor"`
	CheckID        string    `json:"check_id"`
	Kind           string    `json:"kind"`
	DetectorDigest string    `json:"detector_digest,omitempty"`
	Started        time.Time `json:"started"`
	Finished       time.Time `json:"finished"`
	Outcome        string    `json:"outcome"`
	Reason         string    `json:"reason,omitempty"`
	ExitCode       *int      `json:"exit_code,omitempty"`
	OutputDigest   string    `json:"output_digest,omitempty"`
	Excerpt        string    `json:"excerpt,omitempty"`
	SteerVersion   string    `json:"steer_version"`
}

// Passed reports an outcome that counts as a pass.
func (e Execution) Passed() bool {
	return e.Outcome == OutcomePassed || e.Outcome == OutcomePassedCached
}

func executionsPath(dir, identity string) string {
	return filepath.Join(dir, repoKey(identity), executionsName)
}

// evidenceRoot returns the directory that holds execution evidence: the
// isolated root when one is named, else the live state component.
func evidenceRoot(isolated string) (string, error) {
	if isolated != "" {
		return isolated, nil
	}
	return statestore.Prepare(StateComponent)
}

// appendExecutions appends records to the repository's execution stream under
// the evidence root in one write.
func appendExecutions(isolated, identity string, records []Execution) error {
	if len(records) == 0 {
		return nil
	}
	dir, err := evidenceRoot(isolated)
	if err != nil {
		return err
	}
	lines := make([]any, len(records))
	for i, r := range records {
		lines[i] = r
	}
	if err := appendJSONL(executionsPath(dir, identity), lines...); err != nil {
		return fmt.Errorf("record execution: %w", err)
	}
	return nil
}

// appendJSONL appends one JSON line per value to path in one write, creating
// its directory.
func appendJSONL(path string, values ...any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf []byte
	for _, v := range values {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readJSONL passes each line of path to decode, oldest first. A missing file
// has no lines.
func readJSONL(path string, decode func([]byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		decode(scanner.Bytes())
	}
	return scanner.Err()
}

// Executions returns the repository's live execution records, oldest first.
// A line that does not parse is skipped.
func Executions(root string) ([]Execution, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return nil, err
	}
	return executionsIn(statestore.Resolve(StateComponent), identity)
}

func executionsIn(dir, identity string) ([]Execution, error) {
	var out []Execution
	err := readJSONL(executionsPath(dir, identity), func(line []byte) {
		var e Execution
		if json.Unmarshal(line, &e) == nil && e.CheckID != "" {
			out = append(out, e)
		}
	})
	return out, err
}

// LatestExecutions returns each check's most recent execution record.
func LatestExecutions(root string) (map[string]Execution, error) {
	all, err := Executions(root)
	if err != nil {
		return nil, err
	}
	latest := map[string]Execution{}
	for _, e := range all {
		latest[e.CheckID] = e
	}
	return latest, nil
}

// outputRecorder digests every byte written to it and keeps the tail for an
// excerpt. It is safe for concurrent writers.
type outputRecorder struct {
	mu   sync.Mutex
	hash hash.Hash
	tail []byte
}

func newOutputRecorder() *outputRecorder { return &outputRecorder{hash: sha256.New()} }

func (r *outputRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hash.Write(p)
	r.tail = append(r.tail, p...)
	if len(r.tail) > excerptBytes {
		r.tail = append([]byte(nil), r.tail[len(r.tail)-excerptBytes:]...)
	}
	return len(p), nil
}

func (r *outputRecorder) digest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return hex.EncodeToString(r.hash.Sum(nil))
}

func (r *outputRecorder) excerpt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.ToValidUTF8(string(r.tail), "?")
}
