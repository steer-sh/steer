package checks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/steer-sh/steer-cli/internal/procgroup"
	"github.com/steer-sh/steer-cli/internal/version"
)

// DefaultExecTimeout bounds one executor invocation when no parent deadline
// is earlier.
const DefaultExecTimeout = 300 * time.Second

// ExecOptions selects the script checks to run and where their output goes.
// Empty IDs means every script binding in the manifest.
type ExecOptions struct {
	IDs        []string
	Stdout     io.Writer
	Stderr     io.Writer
	Invocation Invocation
	Timeout    time.Duration
}

// Exec runs script checks one after another from the repository root, streams
// their output, and appends one execution record per check. A failing check
// does not stop the ones after it; the deadline does, and the checks it leaves
// are recorded as not executed. The error reports only what kept the executor
// from running checks at all, or from recording them.
func Exec(root string, opts ExecOptions) ([]Execution, error) {
	m, err := Load(root)
	if errors.Is(err, ErrNoManifest) {
		return nil, fmt.Errorf("%w in %s: run `steer check init`", err, root)
	}
	if err != nil {
		return nil, err
	}
	selected, err := selectScripts(m, opts.IDs)
	if err != nil {
		return nil, err
	}
	identity, err := RepoIdentity(root)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, nil
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, fmt.Errorf("steer check exec needs bash: %w", err)
	}
	stdout, stderr := orDiscard(opts.Stdout), orDiscard(opts.Stderr)
	inv := opts.Invocation
	if inv.Source == "" {
		inv.Source = SourceShell
	}
	invocation := newRunID()
	deadline := inv.deadlineWithin(time.Now(), opts.Timeout)
	records := make([]Execution, 0, len(selected))
	expired := false
	for _, d := range selected {
		rec := Execution{
			Version:      ExecutionVersion,
			RunID:        inv.RunID,
			Invocation:   invocation,
			Repo:         identity,
			Source:       inv.Source,
			Harness:      inv.Harness,
			Session:      inv.Session,
			Executor:     ExecutorScript,
			CheckID:      d.ID,
			Kind:         d.Kind,
			Started:      time.Now().UTC(),
			SteerVersion: version.Version,
		}
		if det, err := DetectorOf(root, d); err == nil {
			rec.DetectorDigest = det.Digest
		}
		if expired || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			rec.Outcome, rec.Reason = OutcomeNotExecuted, "the deadline passed before the check started"
		} else {
			runScript(root, bash, d, deadline, stdout, stderr, &rec)
			expired = rec.Outcome == OutcomeTimeout
		}
		rec.Finished = time.Now().UTC()
		if !rec.Passed() {
			fmt.Fprintf(stderr, "steer check %s: %s", d.ID, rec.Outcome)
			if rec.Reason != "" {
				fmt.Fprintf(stderr, " (%s)", rec.Reason)
			}
			fmt.Fprintln(stderr)
		}
		records = append(records, rec)
	}
	return records, appendExecutions(inv.EvidenceDir, identity, records)
}

// selectScripts returns the script bindings named by ids, or all of them.
func selectScripts(m Manifest, ids []string) ([]Declaration, error) {
	if len(ids) == 0 {
		var out []Declaration
		for _, d := range m.Checks {
			if d.Kind == KindScript {
				out = append(out, d)
			}
		}
		return out, nil
	}
	out := make([]Declaration, 0, len(ids))
	for _, id := range ids {
		d, ok := find(m, id)
		if !ok {
			return nil, fmt.Errorf("no check %q is declared", id)
		}
		if d.Kind != KindScript {
			return nil, fmt.Errorf("check %s is a %s binding: it runs through `steer check go-test`", id, d.Kind)
		}
		out = append(out, d)
	}
	return out, nil
}

// runScript runs one script check until it exits or the deadline passes, and
// fills the outcome fields of rec.
func runScript(root, bash string, d Declaration, deadline time.Time, stdout, stderr io.Writer, rec *Execution) {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d.Script))); err != nil {
		rec.Outcome, rec.Reason = OutcomeError, fmt.Sprintf("script %s: %v", d.Script, err)
		return
	}
	ctx := context.Background()
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bash, filepath.FromSlash(d.Script))
	cmd.Dir = root
	procgroup.Set(cmd)
	cmd.WaitDelay = time.Second
	out := newOutputRecorder()
	cmd.Stdout, cmd.Stderr = io.MultiWriter(stdout, out), io.MultiWriter(stderr, out)
	runErr := cmd.Run()
	rec.OutputDigest, rec.Excerpt = out.digest(), out.excerpt()
	if runErr != nil && ctx.Err() != nil {
		rec.Outcome, rec.Reason = OutcomeTimeout, "the deadline stopped the check"
		return
	}
	var exitErr *exec.ExitError
	code := 0
	if errors.As(runErr, &exitErr) {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		rec.Outcome, rec.Reason = OutcomeError, fmt.Sprintf("the check could not run: %v", runErr)
		return
	}
	rec.ExitCode = &code
	switch {
	case code == 0:
		rec.Outcome = OutcomePassed
	case code == unrunnableLow || code == unrunnableTop:
		rec.Outcome, rec.Reason = OutcomeError, fmt.Sprintf("exit %d: a command the check calls could not run", code)
	default:
		rec.Outcome = OutcomeFailed
	}
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
