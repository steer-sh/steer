package checks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/procgroup"
)

// ExecutorVersion names the replay semantics. Raise it when a change to replay
// could turn a stored outcome into a different one; a receipt from another
// executor version is stale.
const ExecutorVersion = 2

// CaseTimeout bounds each setup script and each check run within a case.
var CaseTimeout = 120 * time.Second

const (
	outputCap     = 1 << 20
	excerptBytes  = 2048
	unrunnableLow = 126
	unrunnableTop = 127
)

// Case outcomes.
const (
	// OutcomeMatched is a case whose observed result is the expected one.
	OutcomeMatched = "matched"
	// OutcomeMismatched is a case the check ran on and got wrong.
	OutcomeMismatched = "mismatched"
	// OutcomeInvalid is a case that could not test the check: its setup
	// failed, a command could not run, or it timed out.
	OutcomeInvalid = "invalid"
)

// Run verdicts.
const (
	// VerdictValidated is a run in which every case matched.
	VerdictValidated = "validated"
	// VerdictFailed is a run in which the check got at least one case wrong.
	VerdictFailed = "failed"
	// VerdictUnavailable is a run that could not test the check.
	VerdictUnavailable = "unavailable"
)

// FileDigest is one detector file and the SHA-256 of its bytes.
type FileDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Detector is the tracked inputs of a check and one digest over all of them.
// Decls holds a go-test binding's tracked declarations; Unresolved names what
// its analysis saw but could not track, so the digest does not cover it.
type Detector struct {
	Digest     string       `json:"digest"`
	Files      []FileDigest `json:"files"`
	Decls      []DeclDigest `json:"decls,omitempty"`
	Unresolved []string     `json:"unresolved,omitempty"`
}

// Causes of an invalid case, where replay can tell them apart.
const (
	// CauseIncompatible is a case that no longer fits the current detector,
	// such as a patch that does not apply or code that does not build with
	// the detector. The case needs to be authored again.
	CauseIncompatible = "incompatible-case"
	// CauseEnvironment is a replay host that lacks a tool, module or
	// toolchain the case needs.
	CauseEnvironment = "environment-unavailable"
	// CauseSnapshotUnavailable is a snapshot commit the repository does not
	// hold.
	CauseSnapshotUnavailable = "snapshot-unavailable"
)

// CaseResult is what one case observed. Snapshot is the commit a snapshot case
// started from; Cause classifies an invalid outcome when replay can tell.
type CaseResult struct {
	Name         string `json:"name"`
	Snapshot     string `json:"snapshot,omitempty"`
	ExpectedExit int    `json:"expected_exit"`
	ObservedExit *int   `json:"observed_exit,omitempty"`
	Outcome      string `json:"outcome"`
	Cause        string `json:"cause,omitempty"`
	Reason       string `json:"reason,omitempty"`
	OutputDigest string `json:"output_digest,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
}

// inputDigests digests repository-relative files. A missing file is an error.
func inputDigests(root, id string, paths []string) ([]FileDigest, error) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	var out []FileDigest
	for _, p := range sorted {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("check %s: detector input %s: %w", id, p, err)
		}
		sum := sha256.Sum256(data)
		out = append(out, FileDigest{Path: p, SHA256: hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// DetectorOf digests the tracked inputs of a check as they are now in the
// repository rooted at root. A missing input is an error.
func DetectorOf(root string, d Declaration) (Detector, error) {
	if d.Kind == KindGoTest {
		det, _, err := goDetectorOf(root, d)
		return det, err
	}
	files, err := inputDigests(root, d.ID, append([]string{d.Script}, d.Inputs...))
	if err != nil {
		return Detector{}, err
	}
	all := sha256.New()
	for _, fd := range files {
		fmt.Fprintf(all, "%s\x00%s\n", fd.Path, fd.SHA256)
	}
	return Detector{Digest: hex.EncodeToString(all.Sum(nil)), Files: files}, nil
}

// caseRunner is what one binding kind adds to a case tree and how it runs there.
type caseRunner struct {
	// detector lists the repository files install copies into the tree.
	detector []string
	// protects reports why a case may not write a path, or "" when it may.
	protects func(p string) string
	// install writes the detector into the tree.
	install func(tree string) error
	// run runs the detector in the tree and fills the observed fields of res.
	run func(tree string, c Case, res *CaseResult) (reason string)
}

// Replay runs every case of a check in its own temporary tree holding the
// case's files or snapshot and the current detector. The repository's work
// tree and index are only read. An error means no case could run, such as a
// missing detector input.
func Replay(root string, d Declaration, cases CaseFile) (Detector, []CaseResult, error) {
	bash, bashErr := exec.LookPath("bash")
	var det Detector
	var runner caseRunner
	switch d.Kind {
	case KindGoTest:
		var gd goDetector
		var err error
		det, gd, err = goDetectorOf(root, d)
		if err != nil {
			return Detector{}, nil, err
		}
		if runner, err = goRunner(root, d, gd); err != nil {
			return det, nil, err
		}
	default:
		var err error
		if det, err = DetectorOf(root, d); err != nil {
			return Detector{}, nil, err
		}
		if bashErr != nil {
			return det, nil, fmt.Errorf("replay needs bash: %w", bashErr)
		}
		runner = scriptRunner(root, bash, d)
	}
	results := make([]CaseResult, 0, len(cases.Cases))
	for _, c := range cases.Cases {
		results = append(results, replayCase(root, bash, runner, c))
	}
	return det, results, nil
}

func scriptRunner(root, bash string, d Declaration) caseRunner {
	detectorFiles := map[string]bool{d.Script: true}
	for _, p := range d.Inputs {
		detectorFiles[p] = true
	}
	return caseRunner{
		detector: append([]string{d.Script}, d.Inputs...),
		protects: func(p string) string {
			if detectorFiles[p] {
				return fmt.Sprintf("the case writes detector file %s", p)
			}
			return ""
		},
		install: func(tree string) error {
			return copyDetectorFiles(root, tree, detectorFiles)
		},
		run: func(tree string, c Case, res *CaseResult) string {
			// The tree holds the copy that runs, and copyFile keeps its mode,
			// so the shebang decision is made against the copy.
			name, args := scriptCommand(bash, tree, d.Script)
			code, out, timedOut, err := runCommand(tree, nil, name, args...)
			if timedOut {
				return fmt.Sprintf("the check exceeded %s", CaseTimeout)
			}
			if err != nil {
				return fmt.Sprintf("the check could not run: %v", err)
			}
			sum := sha256.Sum256(out)
			res.ObservedExit = &code
			res.OutputDigest = hex.EncodeToString(sum[:])
			res.Excerpt = excerpt(out)
			if code == unrunnableLow || code == unrunnableTop {
				return fmt.Sprintf("exit %d: a command the check calls could not run", code)
			}
			judge(res, code, out, c.Diagnostic)
			return ""
		},
	}
}

// judge compares an observed exit and output with the case's expectation.
func judge(res *CaseResult, code int, out []byte, diagnostic string) {
	switch {
	case code != res.ExpectedExit:
		res.Outcome, res.Reason = OutcomeMismatched, fmt.Sprintf("exited %d, expected %d", code, res.ExpectedExit)
	case code != 0 && !bytes.Contains(out, []byte(diagnostic)):
		res.Outcome, res.Reason = OutcomeMismatched, fmt.Sprintf("the failure did not print %q", diagnostic)
	default:
		res.Outcome = OutcomeMatched
	}
}

// changedDetectorFile returns the first detector file whose copy in tree no
// longer matches the repository, or "" when every copy matches.
func changedDetectorFile(root, tree string, files []string) string {
	for _, p := range files {
		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return p
		}
		got, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(got, want) {
			return p
		}
	}
	return ""
}

func copyDetectorFiles(root, tree string, files map[string]bool) error {
	for p := range files {
		if err := copyFile(filepath.Join(root, filepath.FromSlash(p)), filepath.Join(tree, filepath.FromSlash(p))); err != nil {
			return fmt.Errorf("copy detector file %s: %w", p, err)
		}
	}
	return nil
}

// replayCase builds one case tree and runs the check in it. A snapshot case
// restores its commit, then the current detector is copied over it, then its
// patch is applied; a patch that changes a detector file makes it invalid.
func replayCase(root, bash string, runner caseRunner, c Case) CaseResult {
	res := CaseResult{Name: c.Name, Snapshot: c.Snapshot, ExpectedExit: *c.ExitCode}
	invalid := func(reason string) CaseResult {
		res.Outcome, res.Reason = OutcomeInvalid, reason
		return res
	}
	because := func(cause, reason string) CaseResult {
		res.Cause = cause
		return invalid(reason)
	}
	for p := range c.Files {
		if reason := runner.protects(p); reason != "" {
			return invalid(reason)
		}
	}
	tmp, err := os.MkdirTemp("", "steer-check-case-")
	if err != nil {
		return invalid(fmt.Sprintf("create case tree: %v", err))
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return invalid(fmt.Sprintf("create case tree: %v", err))
	}
	if c.Snapshot != "" {
		if _, err := exec.LookPath("git"); err != nil {
			return because(CauseEnvironment, "a snapshot case needs git")
		}
		if err := restoreSnapshot(root, c.Snapshot, tmp, tree); errors.Is(err, errSnapshotMissing) {
			return because(CauseSnapshotUnavailable, fmt.Sprintf("commit %s is not in this repository", c.Snapshot))
		} else if err != nil {
			return invalid(fmt.Sprintf("restore snapshot: %v", err))
		}
	}
	if err := runner.install(tree); err != nil {
		return invalid(err.Error())
	}
	if c.Patch != "" {
		if err := applyPatch(tmp, tree, c.patch); err != nil {
			return because(CauseIncompatible, fmt.Sprintf("the patch %s does not apply to snapshot %s: %v", c.Patch, c.Snapshot, err))
		}
		if p := changedDetectorFile(root, tree, runner.detector); p != "" {
			return invalid(fmt.Sprintf("the patch %s changes detector file %s", c.Patch, p))
		}
	}
	for p, content := range c.Files {
		dst := filepath.Join(tree, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return invalid(fmt.Sprintf("write case file %s: %v", p, err))
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return invalid(fmt.Sprintf("write case file %s: %v", p, err))
		}
	}
	if c.Setup != "" {
		if bash == "" {
			return invalid("setup needs bash")
		}
		code, out, timedOut, err := runBash(tree, bash, "-euo", "pipefail", "-c", c.Setup)
		switch {
		case timedOut:
			return invalid(fmt.Sprintf("setup exceeded %s", CaseTimeout))
		case err != nil:
			return invalid(fmt.Sprintf("setup could not run: %v", err))
		case code != 0:
			res.Excerpt = excerpt(out)
			return invalid(fmt.Sprintf("setup exited %d", code))
		}
	}
	if reason := runner.run(tree, c, &res); reason != "" {
		return invalid(reason)
	}
	return res
}

// runBash runs bash with args in dir under CaseTimeout and returns its exit
// code and combined output, capped at outputCap.
func runBash(dir, bash string, args ...string) (code int, out []byte, timedOut bool, err error) {
	return runCommand(dir, nil, bash, args...)
}

// runCommand runs name with args in dir under CaseTimeout, with env added to
// the inherited environment, and returns its exit code and combined output.
func runCommand(dir string, env []string, name string, args ...string) (code int, out []byte, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	procgroup.Set(cmd)
	buf := &cappedBuffer{limit: outputCap}
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return 0, buf.Bytes(), true, nil
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return exit.ExitCode(), buf.Bytes(), false, nil
	}
	if runErr != nil {
		return 0, buf.Bytes(), false, runErr
	}
	return 0, buf.Bytes(), false, nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// excerpt keeps the end of the output, where a failing check prints its reason.
func excerpt(out []byte) string {
	if len(out) > excerptBytes {
		out = out[len(out)-excerptBytes:]
	}
	return strings.ToValidUTF8(string(out), "?")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
