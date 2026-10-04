package checks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// Stop dispositions. Allow, block and yield follow a completed run. The others
// are fail-open states: the turn ends without a verdict from check.sh. Ignored
// and outside-repository are silent; every other state names itself to the
// operator.
const (
	DispositionAllow      = "allow"
	DispositionBlock      = "block"
	DispositionYield      = "yield"
	DispositionTimeout    = "timeout"
	DispositionError      = "error"
	DispositionConcurrent = "concurrent"
	DispositionNotAdopted = "not-adopted"
	DispositionNoOracle   = "no-oracle"
	DispositionNoBash     = "no-bash"
	DispositionBadContext = "bad-context"
	DispositionIgnored    = "ignored"
	DispositionOutside    = "outside-repository"
)

const (
	stopMessagePrefix = "[steer check] "
	// runIDLabel leads the last line of a block reason, which names the run
	// so the transcript entry holding the reason joins to its record.
	runIDLabel = "steer check run: "
	// ContestProtocol is the version of the contest line a block reason carries.
	ContestProtocol = 1
	// diagnosticBytes and diagnosticLines bound the check.sh output a block
	// hands the model.
	diagnosticBytes = 8 << 10
	diagnosticLines = 60
	// stopSessionTTL is how long an idle session's retry state is kept.
	stopSessionTTL = 30 * 24 * time.Hour
)

// StopRequest is a root Stop event in harness-neutral form.
type StopRequest struct {
	Harness  string
	Session  string
	CWD      string
	Timeout  time.Duration
	SteerBin string
	// Locators and StopHookActive are recorded on the run; see OracleRun.
	Locators       map[string]string
	StopHookActive *bool
}

// StopDecision is the Stop runner's answer. Reason is for the model and is set
// only on a block; Message is for the operator. Run is the recorded oracle run,
// when one was recorded.
type StopDecision struct {
	Disposition string
	Reason      string
	Message     string
	Run         *OracleRun
}

// Stop runs an adopted repository's check.sh for a root Stop event and decides
// whether the turn may end. An ordinary failure blocks once; the same failure
// on the same tree for the same repository, harness and session then yields.
// A pass clears that session's retry state. Everything that prevents a verdict
// fails open with its own disposition.
func Stop(req StopRequest) StopDecision {
	if req.CWD == "" || req.Session == "" {
		return StopDecision{Disposition: DispositionBadContext,
			Message: stopMessagePrefix + "the Stop event named no working directory or session; check.sh did not run."}
	}
	root, err := gitToplevel(req.CWD)
	if err != nil {
		return StopDecision{Disposition: DispositionOutside}
	}
	identity, err := RepoIdentity(root)
	if err != nil {
		return stopError(root, err)
	}
	if _, adopted, err := AdoptionOf(identity); err != nil {
		return stopError(identity, err)
	} else if !adopted {
		d := StopDecision{Disposition: DispositionNotAdopted}
		if _, err := os.Stat(ManifestPath(identity)); err == nil && noticeOnce(identity, req, DispositionNotAdopted) {
			d.Message = fmt.Sprintf("%s%s declares Steer checks but is not adopted on this machine, so check.sh did not run. Run `steer check adopt` there to authorize it.",
				stopMessagePrefix, identity)
		}
		return d
	}

	tail := newTailRecorder(diagnosticBytes)
	run, _, err := RunOracle(identity, RunOptions{
		Source: SourceStop, Harness: req.Harness, Session: req.Session, Timeout: req.Timeout,
		Stdout: tail, Stderr: tail, SteerBin: req.SteerBin,
		Locators: req.Locators, StopHookActive: req.StopHookActive,
		Disposition: func(run OracleRun, records []Execution) Verdict {
			disposition := stopDisposition(identity, req, run, records)
			if disposition != DispositionBlock {
				return Verdict{Disposition: disposition}
			}
			return Verdict{Disposition: disposition, ContestProtocol: ContestProtocol,
				BlockReason: blockReason(identity, run, records, tail.excerpt())}
		},
	})
	switch {
	case errors.Is(err, ErrNoOracle):
		d := StopDecision{Disposition: DispositionNoOracle}
		if noticeOnce(identity, req, DispositionNoOracle) {
			d.Message = fmt.Sprintf("%s%s is adopted but has no %s, so the turn was not checked. Restore it, or run `steer check unadopt`.",
				stopMessagePrefix, identity, OracleName)
		}
		return d
	case errors.Is(err, ErrNoBash):
		return StopDecision{Disposition: DispositionNoBash,
			Message: fmt.Sprintf("%sbash was not found, so %s did not run in %s.", stopMessagePrefix, OracleName, identity)}
	case errors.Is(err, ErrConcurrentRun):
		return StopDecision{Disposition: DispositionConcurrent, Run: &run,
			Message: fmt.Sprintf("%s%s did not run for this turn in %s: %s. This turn was not checked.", stopMessagePrefix, OracleName, identity, run.Reason)}
	case err != nil:
		return stopError(identity, err)
	}

	d := StopDecision{Disposition: run.Disposition, Run: &run}
	switch run.Disposition {
	case DispositionBlock:
		d.Reason = run.BlockReason
	case DispositionYield:
		d.Message = fmt.Sprintf("%s%s still fails in %s with the tree unchanged since the last block; the turn ends without another block.",
			stopMessagePrefix, OracleName, identity)
	case DispositionTimeout:
		d.Message = fmt.Sprintf("%s%s passed its %s deadline in %s and was stopped; the turn was not checked. Move slow checks out of %s.",
			stopMessagePrefix, OracleName, run.Deadline.Sub(run.Started).Round(time.Second), identity, OracleName)
	case DispositionError:
		d.Message = fmt.Sprintf("%s%s could not complete in %s (%s); the turn was not checked.", stopMessagePrefix, OracleName, identity, run.Reason)
	}
	return d
}

func stopError(repo string, err error) StopDecision {
	return StopDecision{Disposition: DispositionError,
		Message: fmt.Sprintf("%s%s did not run in %s: %v. The turn was not checked.", stopMessagePrefix, OracleName, repo, err)}
}

// stopDisposition decides a finished run while the repository lock is held,
// and updates the session's retry state to match.
func stopDisposition(identity string, req StopRequest, run OracleRun, records []Execution) string {
	switch {
	case run.ConcurrentWith != "":
		return DispositionConcurrent
	case run.Outcome == OutcomeTimeout:
		return DispositionTimeout
	case run.Outcome == OutcomeError:
		return DispositionError
	}
	state := loadStopState(identity, req.Harness)
	entry := state.Sessions[req.Session]
	if run.Passed() {
		if entry.Failure != "" {
			entry.Failure = ""
			state.put(req.Session, entry)
			_ = saveStopState(identity, req.Harness, state)
		}
		return DispositionAllow
	}
	signature := failureSignature(identity, run, records)
	if entry.Failure == signature {
		return DispositionYield
	}
	entry.Failure = signature
	state.put(req.Session, entry)
	_ = saveStopState(identity, req.Harness, state)
	return DispositionBlock
}

// failureSignature identifies a failure by the run's working tree and by what each
// check reported. Check output is left out: it carries timings, so an
// unchanged failure would never repeat.
func failureSignature(identity string, run OracleRun, records []Execution) string {
	h := sha256.New()
	fmt.Fprintf(h, "tree %s\n", run.Tree)
	fmt.Fprintf(h, "outcome %s\n", run.Outcome)
	if run.ExitCode != nil {
		fmt.Fprintf(h, "exit %d\n", *run.ExitCode)
	}
	lines := make([]string, 0, len(records))
	for _, r := range records {
		lines = append(lines, r.CheckID+" "+r.Outcome)
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Fprintf(h, "check %s\n", line)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// treeSignature digests HEAD, executable state and the content of every path
// with uncommitted changes, untracked files included.
func treeSignature(root string) string {
	h := sha256.New()
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err == nil {
		h.Write(bytes.TrimSpace(head))
	}
	status, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "-uall").Output()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	entries := strings.Split(string(status), "\x00")
	var paths []string
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		paths = append(paths, entry[3:])
		if entry[0] == 'R' || entry[0] == 'C' {
			i++
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(h, "\x00%s\x00", p)
		abs := filepath.Join(root, filepath.FromSlash(p))
		// Match the executable-mode predicate used by scriptCommand.
		mode := byte('?')
		if info, err := os.Stat(abs); err == nil {
			if info.Mode()&0o111 != 0 {
				mode = 'x'
			} else {
				mode = '-'
			}
		}
		h.Write([]byte{mode})
		if data, err := os.ReadFile(abs); err == nil {
			sum := sha256.Sum256(data)
			h.Write(sum[:])
		} else {
			h.Write([]byte("absent"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RunIDLine is the last line of a block reason from run runID. It is the one
// text that joins the transcript entry holding the reason to the run's record.
func RunIDLine(runID string) string { return runIDLabel + runID }

func blockReason(identity string, run OracleRun, records []Execution, output string) string {
	var b strings.Builder
	if run.IntegrationFailure && len(run.Missing) > 0 {
		fmt.Fprintf(&b, "%s passed in %s without running these declared checks: %s.\n", OracleName, identity, strings.Join(run.Missing, ", "))
		b.WriteString("No assertion failed for these missing checks: check.sh does not call the Steer executor for them. ")
		b.WriteString("Add the managed line for each to its sequence (see `steer check guide`):\n")
		lines := map[string]bool{}
		for _, r := range records {
			if r.Executor != ExecutorOracle {
				continue
			}
			line := ExecInvocation
			if r.Kind == KindGoTest {
				line = GoSuiteInvocation
			}
			if !lines[line] {
				lines[line] = true
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
		if run.Reason != "" {
			fmt.Fprintf(&b, "\n%s.\n", run.Reason)
		}
		fmt.Fprintf(&b, "\n%s\n%s%s", contestLine(run.RunID), runIDLabel, run.RunID)
		return b.String()
	}
	exit := "no exit status"
	if run.ExitCode != nil {
		exit = fmt.Sprintf("exit %d", *run.ExitCode)
	}
	if run.IntegrationFailure {
		fmt.Fprintf(&b, "%s exited zero in %s, but verification is incomplete: %s.\n", OracleName, identity, run.Reason)
		for _, r := range records {
			if !r.Passed() && r.Reason != "" {
				fmt.Fprintf(&b, "  %s (%s): %s\n", r.CheckID, r.Outcome, r.Reason)
			}
		}
	} else {
		fmt.Fprintf(&b, "%s failed (%s) in %s. It is this repository's verification oracle, and its verdict applies to this turn's work.\n", OracleName, exit, identity)
	}
	var failing, unreached, skipped []string
	for _, r := range records {
		switch {
		case r.Failed():
			failing = append(failing, fmt.Sprintf("%s (%s)", r.CheckID, r.Outcome))
		case r.Outcome == OutcomeNotExecuted:
			unreached = append(unreached, r.CheckID)
		case r.Outcome == OutcomeSkipped:
			skipped = append(skipped, r.CheckID)
		}
	}
	if len(failing) > 0 {
		fmt.Fprintf(&b, "Declared checks that failed: %s.\n", strings.Join(failing, ", "))
	}
	if len(unreached) > 0 {
		fmt.Fprintf(&b, "Declared checks not reached, because %s stopped before them: %s. They asserted nothing.\n",
			OracleName, strings.Join(unreached, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "Declared checks that skipped themselves: %s.\n", strings.Join(skipped, ", "))
	}
	if lines := lastLines(output, diagnosticLines); lines != "" {
		fmt.Fprintf(&b, "\n%s\n", lines)
	}
	b.WriteString("\nFix the code so check.sh passes. Do not weaken a check, loosen an assertion, skip a test, or add a suppression to reach green.\n")
	b.WriteString("If this failure is not yours to clear (a flaky test, a missing credential, a network dependency), say so and change nothing: " +
		"the turn ends when the tree and the failure are unchanged.")
	fmt.Fprintf(&b, "\n\n%s\n%s%s", contestLine(run.RunID), runIDLabel, run.RunID)
	return b.String()
}

// contestLine tells a blocked agent how to record that this block is wrong for
// its task. The block stands either way.
func contestLine(runID string) string {
	return "If this block is wrong for this task, record why: " +
		steerbin.Command("contest --run "+runID+" '<task-specific reason>'") + ". The block stays."
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func gitToplevel(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("no work tree")
	}
	return root, nil
}

// stopState is one harness's retry state for one repository, by session.
type stopState struct {
	Sessions map[string]stopSession `json:"sessions"`
}

// stopSession holds the signature of the failure the session was last blocked
// on, and the standing notices it has already been given.
type stopSession struct {
	Failure string    `json:"failure,omitempty"`
	Notices []string  `json:"notices,omitempty"`
	At      time.Time `json:"at"`
}

func (s *stopState) put(session string, entry stopSession) {
	entry.At = time.Now().UTC()
	s.Sessions[session] = entry
}

func stopStatePath(dir, identity, harness string) string {
	return filepath.Join(dir, repoKey(identity), "stop-"+harness+".json")
}

func loadStopState(identity, harness string) stopState {
	state := stopState{Sessions: map[string]stopSession{}}
	data, err := os.ReadFile(stopStatePath(statestore.Resolve(StateComponent), identity, harness))
	if err == nil {
		_ = json.Unmarshal(data, &state)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]stopSession{}
	}
	return state
}

// saveStopState replaces the state file whole, dropping sessions idle past
// stopSessionTTL.
func saveStopState(identity, harness string, state stopState) error {
	dir, err := statestore.Prepare(StateComponent)
	if err != nil {
		return err
	}
	for id, entry := range state.Sessions {
		if time.Since(entry.At) > stopSessionTTL {
			delete(state.Sessions, id)
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := stopStatePath(dir, identity, harness)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stop-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// noticeOnce reports whether the session has not yet been given the standing
// notice named by token, and records it as given.
func noticeOnce(identity string, req StopRequest, token string) bool {
	state := loadStopState(identity, req.Harness)
	entry := state.Sessions[req.Session]
	for _, n := range entry.Notices {
		if n == token {
			return false
		}
	}
	entry.Notices = append(entry.Notices, token)
	state.put(req.Session, entry)
	_ = saveStopState(identity, req.Harness, state)
	return true
}
