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
const ExecutorVersion = 1

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
type Detector struct {
	Digest string       `json:"digest"`
	Files  []FileDigest `json:"files"`
}

// CaseResult is what one case observed.
type CaseResult struct {
	Name         string `json:"name"`
	ExpectedExit int    `json:"expected_exit"`
	ObservedExit *int   `json:"observed_exit,omitempty"`
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason,omitempty"`
	OutputDigest string `json:"output_digest,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
}

// detectorPaths lists the repository-relative files a script binding runs.
func detectorPaths(d Declaration) []string {
	paths := append([]string{d.Script}, d.Inputs...)
	sort.Strings(paths)
	return paths
}

// DetectorOf digests the tracked inputs of a script binding as they are now in
// the repository rooted at root. A missing input is an error.
func DetectorOf(root string, d Declaration) (Detector, error) {
	if d.Kind != KindScript {
		return Detector{}, fmt.Errorf("check %s: this steer replays %s bindings only", d.ID, KindScript)
	}
	var det Detector
	all := sha256.New()
	for _, p := range detectorPaths(d) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return Detector{}, fmt.Errorf("check %s: detector input %s: %w", d.ID, p, err)
		}
		sum := sha256.Sum256(data)
		fd := FileDigest{Path: p, SHA256: hex.EncodeToString(sum[:])}
		det.Files = append(det.Files, fd)
		fmt.Fprintf(all, "%s\x00%s\n", fd.Path, fd.SHA256)
	}
	det.Digest = hex.EncodeToString(all.Sum(nil))
	return det, nil
}

// Replay runs every case of a script binding in its own temporary tree holding
// the case's files and a copy of the detector. The repository itself is only
// read. An error means no case could run, such as a missing detector input.
func Replay(root string, d Declaration, cases CaseFile) (Detector, []CaseResult, error) {
	det, err := DetectorOf(root, d)
	if err != nil {
		return Detector{}, nil, err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return det, nil, fmt.Errorf("replay needs bash: %w", err)
	}
	detectorFiles := map[string]bool{}
	for _, p := range detectorPaths(d) {
		detectorFiles[p] = true
	}
	results := make([]CaseResult, 0, len(cases.Cases))
	for _, c := range cases.Cases {
		results = append(results, replayCase(root, bash, d, detectorFiles, c))
	}
	return det, results, nil
}

func replayCase(root, bash string, d Declaration, detectorFiles map[string]bool, c Case) CaseResult {
	res := CaseResult{Name: c.Name, ExpectedExit: *c.ExitCode}
	invalid := func(reason string) CaseResult {
		res.Outcome, res.Reason = OutcomeInvalid, reason
		return res
	}
	for p := range c.Files {
		if detectorFiles[p] {
			return invalid(fmt.Sprintf("the case writes detector file %s", p))
		}
	}
	tmp, err := os.MkdirTemp("", "steer-check-case-")
	if err != nil {
		return invalid(fmt.Sprintf("create case tree: %v", err))
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "tree")
	for p := range detectorFiles {
		if err := copyFile(filepath.Join(root, filepath.FromSlash(p)), filepath.Join(tree, filepath.FromSlash(p))); err != nil {
			return invalid(fmt.Sprintf("copy detector file %s: %v", p, err))
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
	code, out, timedOut, err := runBash(tree, bash, filepath.FromSlash(d.Script))
	if timedOut {
		return invalid(fmt.Sprintf("the check exceeded %s", CaseTimeout))
	}
	if err != nil {
		return invalid(fmt.Sprintf("the check could not run: %v", err))
	}
	sum := sha256.Sum256(out)
	res.ObservedExit = &code
	res.OutputDigest = hex.EncodeToString(sum[:])
	res.Excerpt = excerpt(out)
	switch {
	case code == unrunnableLow || code == unrunnableTop:
		return invalid(fmt.Sprintf("exit %d: a command the check calls could not run", code))
	case code != res.ExpectedExit:
		res.Outcome, res.Reason = OutcomeMismatched, fmt.Sprintf("exited %d, expected %d", code, res.ExpectedExit)
	case code != 0 && !bytes.Contains(out, []byte(c.Diagnostic)):
		res.Outcome, res.Reason = OutcomeMismatched, fmt.Sprintf("the failure did not print %q", c.Diagnostic)
	default:
		res.Outcome = OutcomeMatched
	}
	return res
}

// runBash runs bash with args in dir under CaseTimeout and returns its exit
// code and combined output, capped at outputCap.
func runBash(dir, bash string, args ...string) (code int, out []byte, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
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
