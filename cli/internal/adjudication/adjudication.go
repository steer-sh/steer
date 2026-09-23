// Package adjudication stores the agent's judgments of guard firings and check
// blocks and reports how much of the audit stream they cover. A judgment is a
// reading of one recorded block, kept in the neutral evidence root and naming its
// subject by that record's own identifier. This package judges nothing: it holds
// the record, the identity it was made against, and the arithmetic that says what
// is left to read.
package adjudication

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Component is this corpus's name under the evidence root. It is machine-local and
// partial, like every other component there: it records what this machine read.
const Component = "firing-audits"

// LogName is the append-only receipt stream inside the component directory.
const LogName = "receipts.jsonl"

// The judgments one firing can carry. They describe the block that was recorded,
// never a population: a guard with three prevented blocks and one incorrect block
// has both facts, and neither cancels the other.
const (
	// Prevented means the blocked action would have produced the failure the rule
	// names, judged from the action in its original task context.
	Prevented = "prevented"
	// Incorrect means the blocked action was acceptable work.
	Incorrect = "incorrect"
	// Unresolved means the evidence on hand does not settle which it was. It is a
	// judgment, not a missing one: the case was read and it did not resolve.
	Unresolved = "unresolved"
)

// Judgments lists the values a guard receipt may carry, for a caller validating input.
func Judgments() []string { return []string{Prevented, Incorrect, Unresolved} }

// The kinds of block a receipt can judge. A receipt names its subject explicitly:
// a guard's firing identifier and a check run's identifier are both hexadecimal,
// and a run identifier can fall back to decimal, so no reader tells them apart by
// form. A line that names no subject predates check receipts and reads as a guard.
const (
	SubjectGuard = "guard"
	SubjectCheck = "check"
)

// What one failure in a blocked check run is. A run can report several at once,
// and each is read on its own.
const (
	// FailureCheck is a declared check whose execution did not pass. The check
	// never reached is not one: it asserted nothing.
	FailureCheck = "check"
	// FailureOracle is check.sh itself failing, which covers the steps no declared
	// check owns.
	FailureOracle = "oracle"
	// FailureIntegration is a check.sh that passed while a declared check never
	// reported: the oracle does not call its executor.
	FailureIntegration = "integration"
)

// Whether a reported failure is real. Unresolved is shared with the guard readings.
const (
	Valid   = "valid"
	Invalid = "invalid"
)

// Whether the blocked turn caused the failure. A pre-existing failure can still be
// valid: the defect is real and it is not this turn's.
const (
	Introduced  = "introduced"
	PreExisting = "pre-existing"
)

// What the task did about one failure after the block.
const (
	Fixed        = "fixed"
	RoutedAround = "routed-around"
	Declined     = "declined"
	Interrupted  = "interrupted"
)

// Validities, Attributions and Continuations list the values a check failure's
// three readings may carry, for a caller validating input.
func Validities() []string   { return []string{Valid, Invalid, Unresolved} }
func Attributions() []string { return []string{Introduced, PreExisting, Unresolved} }
func Continuations() []string {
	return []string{Fixed, RoutedAround, Declined, Interrupted, Unresolved}
}

// Failure is the reading of one failure a blocked check run reported. Kind and
// Check name the failure; RecordedOutcome and DetectorDigest are copied from the
// execution record that reported it, so the reading keeps the detector it judged.
type Failure struct {
	Kind            string `json:"kind"`
	Check           string `json:"check,omitempty"`
	RecordedOutcome string `json:"recorded_outcome,omitempty"`
	DetectorDigest  string `json:"detector_digest,omitempty"`

	// Validity is whether the failure is real, Attribution whether the blocked
	// turn introduced it. Either can be Unresolved while the other is settled.
	Validity    string `json:"validity"`
	Attribution string `json:"attribution"`
	// Continuation is what the task did about this failure next. It is set only
	// when the receipt's continuation was inspected.
	Continuation string `json:"continuation,omitempty"`
}

// Receipt is one adjudicated block: a guard firing or a blocked check run.
//
// A guard receipt names its row by FiringID and carries one Judgment. A check
// receipt names its run by RunID and Repo and carries one Failure for each failure
// the run reported, and no Judgment. HookDigest, RuleHash and MasterBuildDigest are
// copied from the row that was read, so the receipt records which implementation it
// judged rather than whichever one is installed when it is read back.
type Receipt struct {
	Subject  string `json:"subject,omitempty"`
	FiringID string `json:"firing_id,omitempty"`
	HookID   string `json:"hook_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Harness  string `json:"harness,omitempty"`

	Judgment  string    `json:"judgment,omitempty"`
	Failures  []Failure `json:"failures,omitempty"`
	Rationale string    `json:"rationale,omitempty"`
	// Evidence names what was read to reach the judgment: a payload sidecar, a
	// transcript path, a commit. A judgment whose evidence nobody can open is not
	// inspectable, so this is the receipt's only support.
	Evidence []string `json:"evidence,omitempty"`

	// ContinuationInspected reports whether the task after the block was read.
	// A correct block whose continuation nobody read leaves benefit unresolved, so
	// the two facts are stored apart.
	ContinuationInspected bool   `json:"continuation_inspected"`
	ContinuationOutcome   string `json:"continuation_outcome,omitempty"`
	// Join is how closely the continuation was tied to the block: one of
	// transcript.JoinCall, JoinTurn or JoinUnavailable. Harnesses reach
	// different granularity and one harness can fail to reach its own, so a
	// reader of this receipt needs the granularity rather than the harness name.
	Join string `json:"join,omitempty"`
	// JoinAnchors are the transcript positions the reading rested on, so a later
	// reader can open what this one opened.
	JoinAnchors *transcript.ContinuationAnchors `json:"join_anchors,omitempty"`
	// JoinSteps is how many continuation steps were available to read. It is the
	// extent of the reading, not a claim that each one was read.
	JoinSteps int `json:"join_steps,omitempty"`
	// InspectedSteps is how many of those steps reached the reader. A rendering
	// under a byte allowance withholds steps from the end, so what the lookup
	// found and what was read apart. Zero means the judgment stated no extent,
	// which is not a claim that none was read.
	InspectedSteps int `json:"inspected_steps,omitempty"`

	// Selection records why this firing was read: which rule of the sampling pass
	// picked it. A later pass advances coverage instead of repeating the same cases.
	Selection string `json:"selection,omitempty"`
	JudgedAt  string `json:"judged_at,omitempty"`

	HookDigest        string `json:"hook_digest,omitempty"`
	RuleHash          string `json:"rule_hash,omitempty"`
	MasterBuildDigest string `json:"master_build_digest,omitempty"`
}

// ID is the identifier the receipt's subject is named by, or "" for a subject
// this build does not know.
func (r Receipt) ID() string {
	switch r.Subject {
	case SubjectGuard:
		return r.FiringID
	case SubjectCheck:
		return r.RunID
	}
	return ""
}

// Dir names the component directory a reader should read. A writer calls
// statestore.Prepare instead, which migrates a legacy corpus before it writes.
func Dir() string { return statestore.Resolve(Component) }

// Load reads every receipt in a component directory, oldest line first. A missing
// stream yields no receipts and no error, so a machine that has adjudicated nothing
// reads the same as one whose store is absent. A line naming no subject reads as a
// guard receipt; a line whose subject this build does not know is skipped.
func Load(dir string) ([]Receipt, error) {
	file, err := os.Open(filepath.Join(dir, LogName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", LogName, err)
	}
	defer file.Close()
	var out []Receipt
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r Receipt
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.Subject == "" {
			r.Subject = SubjectGuard
		}
		if r.ID() == "" {
			continue
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", LogName, err)
	}
	return out, nil
}

// Append writes one receipt to the stream in dir, which must already exist.
//
// A receipt naming no subject, or naming no record of it, is refused: the record's
// identifier is the only identity a judgment can carry, and a judgment attached to
// nothing validates nothing.
func Append(dir string, r Receipt) error {
	switch r.Subject {
	case SubjectGuard:
		if err := validGuard(r); err != nil {
			return err
		}
	case SubjectCheck:
		if err := validCheck(r); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid subject %q: expected %s or %s", r.Subject, SubjectGuard, SubjectCheck)
	}
	// Claiming to have read a continuation no join reached is a false record,
	// and an unread continuation is ordinary: it leaves benefit unresolved.
	if r.ContinuationInspected && (r.Join == "" || r.Join == transcript.JoinUnavailable) {
		return fmt.Errorf("a receipt cannot report an inspected continuation at join %q", r.Join)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to encode the receipt: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, LogName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", LogName, err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("failed to append to %s: %w", LogName, err)
	}
	return nil
}

func validGuard(r Receipt) error {
	if r.FiringID == "" {
		return fmt.Errorf("a receipt must name the firing it judges")
	}
	if r.RunID != "" || len(r.Failures) > 0 {
		return fmt.Errorf("a guard receipt carries no check run or check failures")
	}
	if !oneOf(r.Judgment, Judgments()) {
		return fmt.Errorf("invalid judgment %q: expected one of %s",
			r.Judgment, strings.Join(Judgments(), ", "))
	}
	return nil
}

// validCheck refuses a check receipt that cannot stand as one reading per failure:
// an aggregate judgment, a failure named twice, or a reading outside its values.
func validCheck(r Receipt) error {
	if r.RunID == "" {
		return fmt.Errorf("a receipt must name the check run it judges")
	}
	if r.FiringID != "" || r.Judgment != "" {
		return fmt.Errorf("a check receipt judges each failure and carries no firing or guard judgment")
	}
	if len(r.Failures) == 0 {
		return fmt.Errorf("a check receipt must judge at least one failure of the run")
	}
	seen := map[string]bool{}
	for _, f := range r.Failures {
		switch f.Kind {
		case FailureCheck:
			if f.Check == "" {
				return fmt.Errorf("a %s failure must name its check", FailureCheck)
			}
		case FailureOracle, FailureIntegration:
			if f.Check != "" {
				return fmt.Errorf("a %s failure names no check", f.Kind)
			}
		default:
			return fmt.Errorf("invalid failure kind %q: expected %s, %s or %s",
				f.Kind, FailureCheck, FailureOracle, FailureIntegration)
		}
		key := f.Kind + "\x1f" + f.Check
		if seen[key] {
			return fmt.Errorf("the receipt judges failure %s twice", failureName(f))
		}
		seen[key] = true
		if !oneOf(f.Validity, Validities()) {
			return fmt.Errorf("failure %s: invalid validity %q: expected one of %s",
				failureName(f), f.Validity, strings.Join(Validities(), ", "))
		}
		if !oneOf(f.Attribution, Attributions()) {
			return fmt.Errorf("failure %s: invalid attribution %q: expected one of %s",
				failureName(f), f.Attribution, strings.Join(Attributions(), ", "))
		}
		if f.Continuation != "" && !r.ContinuationInspected {
			return fmt.Errorf("failure %s: a continuation outcome needs an inspected continuation",
				failureName(f))
		}
		if f.Continuation != "" && !oneOf(f.Continuation, Continuations()) {
			return fmt.Errorf("failure %s: invalid continuation %q: expected one of %s",
				failureName(f), f.Continuation, strings.Join(Continuations(), ", "))
		}
	}
	return nil
}

func failureName(f Failure) string {
	if f.Kind == FailureCheck {
		return f.Check
	}
	return f.Kind
}

func oneOf(v string, known []string) bool {
	for _, k := range known {
		if v == k {
			return true
		}
	}
	return false
}

// Latest reduces a stream to one receipt per block of one subject, keeping the last
// line for each. The stream is append-only, so a re-read of the same case
// supersedes the earlier judgment rather than replacing it in place.
func Latest(receipts []Receipt, subject string) map[string]Receipt {
	out := make(map[string]Receipt, len(receipts))
	for _, r := range receipts {
		if r.Subject == subject && r.ID() != "" {
			out[r.ID()] = r
		}
	}
	return out
}
