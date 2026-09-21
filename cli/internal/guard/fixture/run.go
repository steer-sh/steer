package fixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Outcome is what a run answered.
type Outcome string

const (
	OutcomeBlock Outcome = "block"
	OutcomeAllow Outcome = "allow"
)

// MasterState is how a case fared through the generated master.
const (
	// MasterVerified: this guard objected, read off the audit row's hook_name.
	MasterVerified = "verified"
	// MasterShadowed: another guard objected first. Guards run guard-major and
	// the first to object wins, so this is a real and common arrangement. It is
	// never a failure, and it is never proof this guard ran.
	MasterShadowed = "shadowed"
	// MasterFailed: a case the guard blocks alone was allowed through the master.
	MasterFailed = "failed"
	// MasterUnverified: the master pass could not answer — no master, an error,
	// or a block whose objector no audit row names.
	MasterUnverified = "unverified"
	// MasterNotRun: an expect-allow case the master did not decide against this
	// guard. Another guard may legitimately block it, so the master outcome is
	// reported and asserts nothing.
	MasterNotRun = "not-run"
	// MasterBlockedAllow: a case labelled allow that this guard blocked through
	// the master, read off the audit row's hook_name. It names the violated
	// expectation and no cause: identical source can answer differently inside
	// the master, which normalizes one tool call into several and calls main()
	// once per virtual edit.
	MasterBlockedAllow = "blocked-allow"
)

// DefaultTimeout bounds one guard run.
const DefaultTimeout = 20 * time.Second

// CaseResult is one case's two passes.
type CaseResult struct {
	Name           string  `json:"name"`
	Expect         string  `json:"expect"`
	Origin         string  `json:"origin"`
	Individual     Outcome `json:"individual"`
	IndividualPass bool    `json:"individual_pass"`
	Master         Outcome `json:"master,omitempty"`
	MasterObjector string  `json:"master_objector,omitempty"`
	MasterState    string  `json:"master_state"`
	Err            string  `json:"error,omitempty"`
}

// Report is one guard's whole verification.
type Report struct {
	Hook         string       `json:"hook"`
	MasterDigest string       `json:"master_digest,omitempty"`
	Cases        []CaseResult `json:"cases"`
}

// IndividualFailures counts cases the guard alone answered wrongly. The
// individual pass is the guard's whole contract: every case, block and allow.
func (r Report) IndividualFailures() int {
	n := 0
	for _, c := range r.Cases {
		if !c.IndividualPass {
			n++
		}
	}
	return n
}

// MasterFailures counts expect-block cases the master allowed.
func (r Report) MasterFailures() int {
	n := 0
	for _, c := range r.Cases {
		if c.MasterState == MasterFailed {
			n++
		}
	}
	return n
}

// Shadowed counts expect-block cases another guard caught first.
func (r Report) Shadowed() int {
	n := 0
	for _, c := range r.Cases {
		if c.MasterState == MasterShadowed {
			n++
		}
	}
	return n
}

// Incomplete reports whether any case failed to produce an answer. A run that
// could not answer cannot earn tested.
func (r Report) Incomplete() bool {
	for _, c := range r.Cases {
		if c.Err != "" || c.MasterState == MasterUnverified {
			return true
		}
	}
	return len(r.Cases) == 0
}

// BlockedAllow counts cases labelled allow that this guard blocked through the
// master.
func (r Report) BlockedAllow() int {
	n := 0
	for _, c := range r.Cases {
		if c.MasterState == MasterBlockedAllow {
			n++
		}
	}
	return n
}

// Tested reports whether this run establishes that the guard intercepts its
// stored cases. It says nothing about a field block being correct.
func (r Report) Tested() bool {
	return !r.Incomplete() && r.IndividualFailures() == 0 &&
		r.MasterFailures() == 0 && r.BlockedAllow() == 0
}

// Unanswered counts cases that reached no answer. A run reports these apart from
// the cases it answered wrongly: a count of failures alone reads as zero on a
// run that failed only because it could not decide.
func (r Report) Unanswered() int {
	n := 0
	for _, c := range r.Cases {
		if c.Err != "" || c.MasterState == MasterUnverified {
			n++
		}
	}
	return n
}

// NotVerified is the one accounting for a run that did not earn tested. Every
// renderer prints it, so no summary can report a failure with zero counts.
// runner is the caller's own word for the generated master, which the dashboard
// spells differently from the CLI.
func (r Report) NotVerified(runner string) string {
	if len(r.Cases) == 0 {
		return "NOT verified: the guard has no stored cases"
	}
	parts := []string{}
	if n := r.IndividualFailures(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d case(s) the guard answered wrongly", n))
	}
	if n := r.MasterFailures(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d the %s allowed", n, runner))
	}
	if n := r.BlockedAllow(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d labelled allow that this guard blocked through the %s", n, runner))
	}
	if n := r.Unanswered(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d that reached no answer (%s)", n, r.firstError()))
	}
	if len(parts) == 0 {
		return "NOT verified"
	}
	return "NOT verified: " + strings.Join(parts, ", ")
}

// firstError names why a case reached no answer, so the summary carries a reason
// rather than a count the reader cannot act on.
func (r Report) firstError() string {
	for _, c := range r.Cases {
		if c.Err != "" {
			return c.Err
		}
	}
	return "no master to verify against"
}

// Runner replays fixtures against byte-identical copies of a guard and the
// generated master, inside a temporary store.
//
// The copies are the whole point. steer_audit_log writes beside the master's own
// __file__, so running the installed master would append synthetic firings to
// the operator's live audit stream, which the field scorer reads. The copied
// master keeps its bytes, so MasterDigest still identifies the installed build.
type Runner struct {
	Python  string
	Timeout time.Duration
}

func (r Runner) python() string {
	if strings.TrimSpace(r.Python) != "" {
		return r.Python
	}
	return "python3"
}

func (r Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Run replays every case. masterPath may be empty, in which case the master pass
// reports unverified rather than being skipped silently.
func (r Runner) Run(guardPath, masterPath string, file File) (Report, error) {
	if err := file.Validate(); err != nil {
		return Report{}, err
	}

	root, err := os.MkdirTemp("", "steer-fixture-")
	if err != nil {
		return Report{}, fmt.Errorf("failed to create fixture sandbox: %w", err)
	}
	defer os.RemoveAll(root)

	storeDir := filepath.Join(root, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return Report{}, fmt.Errorf("failed to create fixture store: %w", err)
	}

	guardCopy := filepath.Join(storeDir, file.Hook+".py")
	if err := copyFile(guardPath, guardCopy); err != nil {
		return Report{}, err
	}

	masterCopy := ""
	if strings.TrimSpace(masterPath) != "" {
		masterCopy = filepath.Join(storeDir, "steer-hooks.py")
		if err := copyFile(masterPath, masterCopy); err != nil {
			return Report{}, err
		}
	}

	report := Report{Hook: file.Hook, MasterDigest: FileDigest(masterPath)}
	auditPath := filepath.Join(storeDir, "steer-audit.jsonl")

	for i, c := range file.Cases {
		row := CaseResult{Name: c.Name, Expect: c.Expect, Origin: c.Origin, MasterState: MasterNotRun}

		sandbox := filepath.Join(root, fmt.Sprintf("case-%d", i))
		payload, err := materialize(sandbox, c)
		if err != nil {
			row.Err = err.Error()
			report.Cases = append(report.Cases, row)
			continue
		}

		outcome, err := r.invoke(guardCopy, payload, sandbox, "")
		if err != nil {
			row.Err = err.Error()
			report.Cases = append(report.Cases, row)
			continue
		}
		row.Individual = outcome
		row.IndividualPass = string(outcome) == c.Expect

		if c.Expect == ExpectAllow {
			if masterCopy == "" {
				row.MasterState = MasterUnverified
				row.Err = "no master hook to verify against"
				report.Cases = append(report.Cases, row)
				continue
			}
			masterOutcome, err := r.invoke(masterCopy, payload, sandbox, auditPath)
			if err != nil {
				row.MasterState = MasterUnverified
				row.Err = err.Error()
				report.Cases = append(report.Cases, row)
				continue
			}
			row.Master = masterOutcome
			if masterOutcome == OutcomeAllow {
				report.Cases = append(report.Cases, row)
				continue
			}
			// The master blocked a case labelled allow. Which guard objected is
			// the whole answer, and the runner already holds it.
			objector, err := lastObjector(auditPath)
			if err != nil {
				row.MasterState = MasterUnverified
				row.Err = err.Error()
				report.Cases = append(report.Cases, row)
				continue
			}
			if objector == "" {
				row.MasterState = MasterUnverified
				row.Err = "the master blocked an allow case and no audit row names the guard that objected"
				report.Cases = append(report.Cases, row)
				continue
			}
			row.MasterObjector = objector
			if objector == file.Hook {
				row.MasterState = MasterBlockedAllow
			}
			report.Cases = append(report.Cases, row)
			continue
		}

		if masterCopy == "" {
			row.MasterState = MasterUnverified
			row.Err = "no master hook to verify against"
			report.Cases = append(report.Cases, row)
			continue
		}

		masterOutcome, err := r.invoke(masterCopy, payload, sandbox, auditPath)
		if err != nil {
			row.MasterState = MasterUnverified
			row.Err = err.Error()
			report.Cases = append(report.Cases, row)
			continue
		}
		row.Master = masterOutcome
		if masterOutcome == OutcomeAllow {
			row.MasterState = MasterFailed
			report.Cases = append(report.Cases, row)
			continue
		}
		objector, err := lastObjector(auditPath)
		if err != nil || objector == "" {
			row.MasterState = MasterUnverified
			if row.Err == "" {
				row.Err = "the master blocked and no audit row names the guard that objected"
			}
			report.Cases = append(report.Cases, row)
			continue
		}
		row.MasterObjector = objector
		if objector == file.Hook {
			row.MasterState = MasterVerified
		} else {
			row.MasterState = MasterShadowed
		}
		report.Cases = append(report.Cases, row)
	}

	return report, nil
}

// invoke runs one script on one payload. auditPath is truncated first when set,
// so the rows a run leaves are that run's alone.
func (r Runner) invoke(script string, payload []byte, cwd, auditPath string) (Outcome, error) {
	if auditPath != "" {
		if err := os.Remove(auditPath); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to clear the fixture audit log: %w", err)
		}
	}

	var buf strings.Builder
	cmd := exec.Command(r.python(), script)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start %s: %w", filepath.Base(script), err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(r.timeout()):
		_ = cmd.Process.Kill()
		<-done
		return "", fmt.Errorf("%s exceeded %s", filepath.Base(script), r.timeout())
	}

	text := strings.TrimSpace(buf.String())
	if blocked := parseBlock(text); blocked {
		return OutcomeBlock, nil
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return "", fmt.Errorf("%s failed: %w", filepath.Base(script), runErr)
	}
	if runErr != nil {
		return "", fmt.Errorf("%s exited non-zero: %s", filepath.Base(script), text)
	}
	return OutcomeAllow, nil
}

// parseBlock mirrors runtime.py's steer_parse_block, compact-text fallback
// included: a reply this cannot decode that still says block is a block.
func parseBlock(output string) bool {
	if output == "" {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		decision, _ := parsed["decision"].(string)
		return decision == "block"
	}
	return strings.Contains(strings.Join(strings.Fields(output), ""), `"decision":"block"`)
}

// lastObjector reads the hook_name of the newest row in an isolated audit log.
func lastObjector(auditPath string) (string, error) {
	data, err := os.ReadFile(auditPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read the fixture audit log: %w", err)
	}
	name := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			HookName string `json:"hook_name"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.HookName != "" {
			name = row.HookName
		}
	}
	return name, nil
}

// materialize writes a case's filesystem context into its own sandbox and
// rewrites the payload's relative paths against it. An absolute path is left
// alone: a case that names one meant that path.
func materialize(sandbox string, c Case) ([]byte, error) {
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create case sandbox: %w", err)
	}
	for rel, content := range c.Files {
		target := filepath.Join(sandbox, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("failed to create %s: %w", rel, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", rel, err)
		}
	}

	payload := clone(c.Payload)
	rebase(payload, "cwd", sandbox)
	if input, ok := payload["tool_input"].(map[string]any); ok {
		rebase(input, "file_path", sandbox)
		rebase(input, "notebook_path", sandbox)
	}
	if _, ok := payload["cwd"]; !ok {
		payload["cwd"] = sandbox
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode case payload: %w", err)
	}
	return data, nil
}

func rebase(holder map[string]any, key, sandbox string) {
	value, ok := holder[key].(string)
	if !ok || value == "" || filepath.IsAbs(value) {
		return
	}
	holder[key] = filepath.Join(sandbox, filepath.FromSlash(value))
}

func clone(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for k, v := range value {
		if nested, ok := v.(map[string]any); ok {
			out[k] = clone(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filepath.Base(src), err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("failed to write %s: %w", filepath.Base(dst), err)
	}
	return nil
}
