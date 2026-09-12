package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/procgroup"
	"github.com/steer-sh/steer-cli/internal/version"
)

// GoSuiteOptions configures one run of the repository's Go suite. Args follow
// `go test -json`; Dir is where go runs and defaults to the repository root.
type GoSuiteOptions struct {
	Dir        string
	Args       []string
	Stdout     io.Writer
	Stderr     io.Writer
	Invocation Invocation
	Timeout    time.Duration
}

// RunGoSuite runs `go test -json` once, prints the build errors, failing test
// output and package results that plain `go test` prints, in event order, and
// returns its exit status. Every go-test binding whose package the
// run reported gets one execution record read from that run; a binding whose
// package the run never reported gets none. The error reports only a suite
// that could not start or a run that could not be recorded.
func RunGoSuite(root string, opts GoSuiteOptions) (int, []Execution, error) {
	m, err := Load(root)
	if errors.Is(err, ErrNoManifest) {
		return 1, nil, fmt.Errorf("%w in %s: run `steer check init`", err, root)
	}
	if err != nil {
		return 1, nil, err
	}
	identity, err := RepoIdentity(root)
	if err != nil {
		return 1, nil, err
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return 1, nil, fmt.Errorf("steer check go-test needs the go toolchain: %w", err)
	}
	inv := opts.Invocation
	if inv.Source == "" {
		inv.Source = SourceShell
	}
	base := Execution{
		Version:      ExecutionVersion,
		RunID:        inv.RunID,
		Invocation:   newRunID(),
		Repo:         identity,
		Source:       inv.Source,
		Harness:      inv.Harness,
		Session:      inv.Session,
		Executor:     ExecutorGoTest,
		Kind:         KindGoTest,
		SteerVersion: version.Version,
	}
	start := time.Now()
	reader := newGoSuiteReader(orDiscard(opts.Stdout))
	var records []Execution
	for _, d := range m.Checks {
		if d.Kind != KindGoTest {
			continue
		}
		rec := base
		rec.CheckID = d.ID
		if det, err := DetectorOf(root, d); err == nil {
			rec.DetectorDigest = det.Digest
		}
		importPath, err := packageImportPath(root, d.Package)
		if err != nil {
			rec.Started, rec.Finished = start.UTC(), start.UTC()
			rec.Outcome, rec.Reason = OutcomeUnavailable, fmt.Sprintf("package %s: %v", d.Package, err)
			records = append(records, rec)
			continue
		}
		reader.bind(importPath, d.Test, rec)
	}

	ctx := context.Background()
	if deadline := inv.deadlineWithin(start, opts.Timeout); !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	dir := opts.Dir
	if dir == "" {
		dir = root
	}
	cmd := exec.CommandContext(ctx, goBin, append([]string{"test", "-json"}, opts.Args...)...)
	cmd.Dir = dir
	procgroup.Set(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = reader, orDiscard(opts.Stderr)
	runErr := cmd.Run()
	reader.finish()
	timedOut := runErr != nil && ctx.Err() != nil
	code := 0
	var exitErr *exec.ExitError
	switch {
	case timedOut:
		code = 1
		fmt.Fprintln(orDiscard(opts.Stderr), "steer check go-test: the deadline stopped go test")
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case runErr != nil:
		return 1, nil, fmt.Errorf("go test could not run: %w", runErr)
	}
	records = append(records, reader.records(time.Now(), timedOut)...)
	sort.Slice(records, func(i, j int) bool { return records[i].CheckID < records[j].CheckID })
	return code, records, appendExecutions(identity, records)
}

// goSuiteEvent is one line of `go test -json`, including the build events.
type goSuiteEvent struct {
	Time        time.Time `json:"Time"`
	Action      string    `json:"Action"`
	Package     string    `json:"Package"`
	Test        string    `json:"Test"`
	Output      string    `json:"Output"`
	FailedBuild string    `json:"FailedBuild"`
}

type goPackageState struct {
	cached      bool
	failedBuild bool
}

type boundGoTest struct {
	record   Execution
	action   string
	started  time.Time
	finished time.Time
	output   *outputRecorder
}

// goSuiteReader consumes `go test -json` as it streams. It renders the plain
// output and collects each bound test's result and output.
type goSuiteReader struct {
	out     io.Writer
	partial []byte
	bound   map[string]map[string][]*boundGoTest
	pkgs    map[string]*goPackageState
	pending map[string]*bytes.Buffer
}

func newGoSuiteReader(out io.Writer) *goSuiteReader {
	return &goSuiteReader{
		out:     out,
		bound:   map[string]map[string][]*boundGoTest{},
		pkgs:    map[string]*goPackageState{},
		pending: map[string]*bytes.Buffer{},
	}
}

func (r *goSuiteReader) bind(importPath, test string, rec Execution) {
	if r.bound[importPath] == nil {
		r.bound[importPath] = map[string][]*boundGoTest{}
	}
	r.bound[importPath][test] = append(r.bound[importPath][test], &boundGoTest{record: rec, output: newOutputRecorder()})
}

func (r *goSuiteReader) Write(p []byte) (int, error) {
	r.partial = append(r.partial, p...)
	for {
		i := bytes.IndexByte(r.partial, '\n')
		if i < 0 {
			break
		}
		r.line(r.partial[:i])
		r.partial = r.partial[i+1:]
	}
	return len(p), nil
}

func (r *goSuiteReader) finish() {
	if len(r.partial) > 0 {
		r.line(r.partial)
		r.partial = nil
	}
}

// framing reports the lines that only verbose output shows.
func framing(output string) bool {
	for _, prefix := range []string{"=== RUN", "=== PAUSE", "=== CONT", "=== NAME"} {
		if strings.HasPrefix(output, prefix) {
			return true
		}
	}
	return false
}

func (r *goSuiteReader) line(line []byte) {
	var ev goSuiteEvent
	if json.Unmarshal(line, &ev) != nil || ev.Action == "" {
		r.out.Write(append(append([]byte(nil), line...), '\n'))
		return
	}
	switch {
	case ev.Action == "build-output":
		io.WriteString(r.out, ev.Output)
		return
	case ev.Package == "":
		return
	}
	ps := r.pkgs[ev.Package]
	if ps == nil {
		ps = &goPackageState{}
		r.pkgs[ev.Package] = ps
	}
	if ev.Test == "" {
		r.packageEvent(ev, ps)
		return
	}
	top, _, _ := strings.Cut(ev.Test, "/")
	for _, bt := range r.bound[ev.Package][top] {
		if ev.Action == "output" {
			bt.output.Write([]byte(ev.Output))
		}
		if ev.Test == top {
			switch ev.Action {
			case "run":
				bt.started = ev.Time
			case "pass", "fail", "skip":
				bt.action, bt.finished = ev.Action, ev.Time
			}
		}
	}
	key := ev.Package + "\x00" + top
	switch ev.Action {
	case "output":
		if !framing(ev.Output) {
			if r.pending[key] == nil {
				r.pending[key] = &bytes.Buffer{}
			}
			r.pending[key].WriteString(ev.Output)
		}
	case "pass", "skip":
		if ev.Test == top {
			delete(r.pending, key)
		}
	case "fail":
		if ev.Test == top {
			if buf := r.pending[key]; buf != nil {
				r.out.Write(buf.Bytes())
			}
			delete(r.pending, key)
		}
	}
}

// packageEvent handles package-level output and the package's final action.
// A failed package prints any test output no test-level failure printed, such
// as the output of a test that panicked.
func (r *goSuiteReader) packageEvent(ev goSuiteEvent, ps *goPackageState) {
	switch ev.Action {
	case "output":
		if strings.HasPrefix(ev.Output, "ok") && strings.Contains(ev.Output, "(cached)") {
			ps.cached = true
		}
		if ev.Output != "PASS\n" {
			io.WriteString(r.out, ev.Output)
		}
	case "pass", "skip", "fail":
		if ev.FailedBuild != "" {
			ps.failedBuild = true
		}
		prefix := ev.Package + "\x00"
		keys := make([]string, 0)
		for key := range r.pending {
			if strings.HasPrefix(key, prefix) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if ev.Action == "fail" {
				r.out.Write(r.pending[key].Bytes())
			}
			delete(r.pending, key)
		}
	}
}

// records turns each bound test in a reported package into an execution.
func (r *goSuiteReader) records(end time.Time, timedOut bool) []Execution {
	var out []Execution
	for importPath, tests := range r.bound {
		ps, seen := r.pkgs[importPath]
		if !seen {
			continue
		}
		for _, bt := range flatten(tests) {
			rec := bt.record
			rec.Started, rec.Finished = bt.started.UTC(), bt.finished.UTC()
			if bt.started.IsZero() {
				rec.Started = end.UTC()
			}
			if bt.finished.IsZero() {
				rec.Finished = end.UTC()
			}
			switch bt.action {
			case "pass":
				rec.Outcome = OutcomePassed
				if ps.cached {
					rec.Outcome = OutcomePassedCached
				}
			case "fail":
				rec.Outcome = OutcomeFailed
			case "skip":
				rec.Outcome = OutcomeSkipped
			default:
				switch {
				case ps.failedBuild:
					rec.Outcome, rec.Reason = OutcomeUnavailable, "the package failed to build"
				case timedOut:
					rec.Outcome, rec.Reason = OutcomeNotExecuted, "the deadline stopped go test before the test reported"
				default:
					rec.Outcome, rec.Reason = OutcomeUnavailable, "the package ran without a result for the test: it may be renamed, or excluded by -run"
				}
			}
			if bt.action != "" {
				rec.OutputDigest, rec.Excerpt = bt.output.digest(), bt.output.excerpt()
			}
			out = append(out, rec)
		}
	}
	return out
}

func flatten(tests map[string][]*boundGoTest) []*boundGoTest {
	var out []*boundGoTest
	for _, list := range tests {
		out = append(out, list...)
	}
	return out
}
