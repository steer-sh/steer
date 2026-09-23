package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/procgroup"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/version"
)

const runsName = "runs.jsonl"

// OracleRunVersion is the oracle-run record format.
const OracleRunVersion = 2

// Invocation sources that a Steer parent names.
const (
	SourceRun   = "run"
	SourceAdopt = "adopt"
	SourceStop  = "stop"
)

// ExecutorOracle marks an execution record that the parent oracle wrote for a
// declared check no executor reported under its run.
const ExecutorOracle = "check-run"

// ErrNoOracle reports a repository without check.sh at its root.
var ErrNoOracle = errors.New("no " + OracleName + " at the repository root")

// ErrNoBash reports a machine where bash cannot be found.
var ErrNoBash = errors.New("bash was not found")

// SelfSteerBin is the STEER_BIN a caller passes to check.sh: "" when the operator
// set STEER_BIN, which then stays, otherwise the running executable.
func SelfSteerBin() string {
	if os.Getenv("STEER_BIN") != "" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// RunOptions configures one run of the repository's oracle. SteerBin, when
// set, becomes STEER_BIN for check.sh. EvidenceDir, when set, isolates the
// run: its executor records and its own record go under that root, never the
// live one. Disposition, when set, is called with the finished run and its
// records while the repository lock is held; its result is recorded as the
// run's disposition.
type RunOptions struct {
	Source      string
	Harness     string
	Session     string
	Timeout     time.Duration
	Stdout      io.Writer
	Stderr      io.Writer
	SteerBin    string
	EvidenceDir string
	// Locators and StopHookActive are copied onto the run record.
	Locators       map[string]string
	StopHookActive *bool
	// Disposition decides a finished run while the lock is held. It returns
	// what the caller did with it and, for a block, the reason it sends.
	Disposition func(OracleRun, []Execution) (disposition, blockReason string)
}

// OracleRun is the aggregate outcome of one check.sh run by a Steer parent. The
// per-check results are execution records under the same RunID. Missing names
// the declared checks that no executor reported; after a passing check.sh they
// are an integration failure, otherwise checks the run never reached.
// ConcurrentWith names the run that held the repository when this one was
// refused; such a run is not executed and has no per-check records.
// Disposition is what the caller did with the result, such as a Stop runner's
// block or yield, and BlockReason is the text a block sent the model.
// Locators are the Stop payload's transcript locators by key presence, from
// version 2 on. Tree is the working-tree signature after check.sh finished.
type OracleRun struct {
	Version            int               `json:"version"`
	RunID              string            `json:"run_id"`
	Repo               string            `json:"repo"`
	Source             string            `json:"source"`
	Harness            string            `json:"harness,omitempty"`
	Session            string            `json:"session,omitempty"`
	Started            time.Time         `json:"started"`
	Finished           time.Time         `json:"finished"`
	Deadline           time.Time         `json:"deadline"`
	Outcome            string            `json:"outcome"`
	Reason             string            `json:"reason,omitempty"`
	ExitCode           *int              `json:"exit_code,omitempty"`
	OutputDigest       string            `json:"output_digest,omitempty"`
	Excerpt            string            `json:"excerpt,omitempty"`
	Declared           []string          `json:"declared,omitempty"`
	Missing            []string          `json:"missing,omitempty"`
	IntegrationFailure bool              `json:"integration_failure,omitempty"`
	ConcurrentWith     string            `json:"concurrent_with,omitempty"`
	Disposition        string            `json:"disposition,omitempty"`
	BlockReason        string            `json:"block_reason,omitempty"`
	Locators           map[string]string `json:"locators,omitempty"`
	StopHookActive     *bool             `json:"stop_hook_active,omitempty"`
	Tree               string            `json:"tree,omitempty"`
	SteerVersion       string            `json:"steer_version"`
}

// Passed reports a check.sh that exited 0 with every declared check reported.
func (r OracleRun) Passed() bool {
	return r.Outcome == OutcomePassed && !r.IntegrationFailure
}

func runsPath(dir, identity string) string {
	return filepath.Join(dir, repoKey(identity), runsName)
}

// RunOracle runs `bash check.sh` from the repository root under a new run ID
// and the STEER_CHECK_* contract, and records the aggregate outcome. A
// declared check with no executor record under the run ID gets a not-executed
// record from the parent. It returns the run and every record under its ID;
// the error reports a run that could not start or could not be recorded.
//
// One run at a time holds the repository. A run refused because another holds
// it is recorded as not executed, naming the holder, and returns that record
// with an error wrapping ErrConcurrentRun.
func RunOracle(root string, opts RunOptions) (OracleRun, []Execution, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return OracleRun{}, nil, err
	}
	if _, err := os.Stat(OraclePath(root)); errors.Is(err, os.ErrNotExist) {
		return OracleRun{}, nil, fmt.Errorf("%w: %s", ErrNoOracle, identity)
	} else if err != nil {
		return OracleRun{}, nil, err
	}
	var declared []Declaration
	if m, err := Load(root); err == nil {
		declared = m.Checks
	} else if !errors.Is(err, ErrNoManifest) {
		return OracleRun{}, nil, err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return OracleRun{}, nil, fmt.Errorf("%w: %v", ErrNoBash, err)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	start := time.Now()
	deadline := start.Add(timeout)
	run := OracleRun{
		Version:        OracleRunVersion,
		RunID:          newRunID(),
		Repo:           identity,
		Source:         opts.Source,
		Harness:        opts.Harness,
		Session:        opts.Session,
		Started:        start.UTC(),
		Deadline:       deadline.UTC(),
		Locators:       opts.Locators,
		StopHookActive: opts.StopHookActive,
		SteerVersion:   version.Version,
	}
	if run.Source == "" {
		run.Source = SourceRun
	}
	for _, d := range declared {
		run.Declared = append(run.Declared, d.ID)
	}
	dir, err := evidenceRoot(opts.EvidenceDir)
	if err != nil {
		return run, nil, err
	}

	lock, holder, err := acquireRepoLock(identity, LockHolder{RunID: run.RunID, PID: os.Getpid(), Source: run.Source, Started: run.Started})
	if errors.Is(err, ErrConcurrentRun) {
		run.Finished = time.Now().UTC()
		run.Outcome = OutcomeNotExecuted
		run.ConcurrentWith = holder.RunID
		run.Reason = concurrentReason(holder)
		if opts.Disposition != nil {
			run.Disposition, run.BlockReason = opts.Disposition(run, nil)
		}
		if err := appendJSONL(runsPath(dir, identity), run); err != nil {
			return run, nil, fmt.Errorf("record oracle run: %w", err)
		}
		return run, nil, fmt.Errorf("%w: %s", ErrConcurrentRun, run.Reason)
	}
	if err != nil {
		return run, nil, err
	}
	defer lock.release()

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, OracleName)
	cmd.Dir = root
	cmd.Env = oracleEnv(os.Environ(), run, deadline.Add(-childGrace(timeout)), opts)
	procgroup.Set(cmd)
	cmd.WaitDelay = time.Second
	out := newOutputRecorder()
	cmd.Stdout = io.MultiWriter(orDiscard(opts.Stdout), out)
	cmd.Stderr = io.MultiWriter(orDiscard(opts.Stderr), out)
	runErr := cmd.Run()
	run.Finished = time.Now().UTC()
	run.OutputDigest, run.Excerpt = out.digest(), out.excerpt()
	classifyOracle(&run, runErr, ctx.Err() != nil)
	run.Tree = treeSignature(identity)

	all, err := executionsIn(dir, identity)
	if err != nil {
		return run, nil, err
	}
	var records []Execution
	reported := map[string]bool{}
	for _, e := range all {
		if e.RunID == run.RunID {
			records = append(records, e)
			reported[e.CheckID] = true
		}
	}
	var unreported []Execution
	for _, d := range declared {
		if reported[d.ID] {
			continue
		}
		run.Missing = append(run.Missing, d.ID)
		rec := Execution{
			Version:      ExecutionVersion,
			RunID:        run.RunID,
			Invocation:   run.RunID,
			Repo:         identity,
			Source:       run.Source,
			Harness:      run.Harness,
			Session:      run.Session,
			Executor:     ExecutorOracle,
			CheckID:      d.ID,
			Kind:         d.Kind,
			Started:      run.Finished,
			Finished:     run.Finished,
			Outcome:      OutcomeNotExecuted,
			Reason:       unreportedReason(run.Outcome),
			SteerVersion: version.Version,
		}
		if det, err := DetectorOf(root, d); err == nil {
			rec.DetectorDigest = det.Digest
		}
		unreported = append(unreported, rec)
	}
	run.IntegrationFailure = run.Outcome == OutcomePassed && len(run.Missing) > 0
	if err := appendExecutions(opts.EvidenceDir, identity, unreported); err != nil {
		return run, nil, err
	}
	records = append(records, unreported...)
	if opts.Disposition != nil {
		run.Disposition, run.BlockReason = opts.Disposition(run, records)
	}
	if err := appendJSONL(runsPath(dir, identity), run); err != nil {
		return run, nil, fmt.Errorf("record oracle run: %w", err)
	}
	return run, records, nil
}

func concurrentReason(holder LockHolder) string {
	if holder.RunID == "" {
		return "another " + OracleName + " run holds the repository"
	}
	return fmt.Sprintf("%s run %s (source %s, pid %d) holds the repository", OracleName, holder.RunID, holder.Source, holder.PID)
}

// classifyOracle fills the outcome fields of run from how check.sh ended.
func classifyOracle(run *OracleRun, runErr error, deadlinePassed bool) {
	var exitErr *exec.ExitError
	switch {
	case runErr != nil && deadlinePassed:
		run.Outcome, run.Reason = OutcomeTimeout, "the deadline stopped "+OracleName
	case errors.As(runErr, &exitErr):
		code := exitErr.ExitCode()
		run.ExitCode = &code
		if code == unrunnableLow || code == unrunnableTop {
			run.Outcome, run.Reason = OutcomeError, fmt.Sprintf("exit %d: a command %s calls could not run", code, OracleName)
		} else {
			run.Outcome = OutcomeFailed
		}
	case runErr != nil:
		run.Outcome, run.Reason = OutcomeError, fmt.Sprintf("%s could not run: %v", OracleName, runErr)
	default:
		code := 0
		run.ExitCode = &code
		run.Outcome = OutcomePassed
	}
}

func unreportedReason(oracleOutcome string) string {
	switch oracleOutcome {
	case OutcomePassed:
		return OracleName + " passed without running this check: the oracle does not call its executor"
	case OutcomeTimeout:
		return "the deadline stopped " + OracleName + " before this check reported"
	default:
		return OracleName + " stopped before this check reported"
	}
}

// childGrace is how much earlier than the parent's deadline the executors'
// deadline falls, so a hung check is recorded before the group is stopped.
func childGrace(timeout time.Duration) time.Duration {
	if g := timeout / 10; g < 2*time.Second {
		return g
	}
	return 2 * time.Second
}

// oracleEnv returns base without any inherited STEER_CHECK_* contract, plus the
// contract for run. It replaces STEER_BIN only when opts names one.
func oracleEnv(base []string, run OracleRun, childDeadline time.Time, opts RunOptions) []string {
	env := make([]string, 0, len(base)+7)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "STEER_CHECK_") || (opts.SteerBin != "" && name == "STEER_BIN") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		EnvRunID+"="+run.RunID,
		EnvSource+"="+run.Source,
		EnvDeadline+"="+childDeadline.UTC().Format(time.RFC3339Nano),
	)
	if run.Harness != "" {
		env = append(env, EnvHarness+"="+run.Harness)
	}
	if run.Session != "" {
		env = append(env, EnvSession+"="+run.Session)
	}
	if opts.EvidenceDir != "" {
		env = append(env, EnvEvidenceDir+"="+opts.EvidenceDir)
	}
	if opts.SteerBin != "" {
		env = append(env, "STEER_BIN="+opts.SteerBin)
	}
	return env
}

// OracleRuns returns the repository's live oracle runs, oldest first. A line
// that does not parse is skipped.
func OracleRuns(root string) ([]OracleRun, error) {
	identity, err := RepoIdentity(root)
	if err != nil {
		return nil, err
	}
	var out []OracleRun
	err = readJSONL(runsPath(statestore.Resolve(StateComponent), identity), func(line []byte) {
		var r OracleRun
		if json.Unmarshal(line, &r) == nil && r.RunID != "" {
			out = append(out, r)
		}
	})
	return out, err
}
