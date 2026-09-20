// Package adjudication stores the agent's judgments of guard firings and reports
// how much of the audit stream they cover. A judgment is a reading of one recorded
// block, kept in the neutral evidence root and naming its subject by that row's own
// identifier. This package judges nothing: it holds the record, the identity it was
// made against, and the arithmetic that says what is left to read.
package adjudication

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/statestore"
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

// Judgments lists the values a receipt may carry, for a caller validating input.
func Judgments() []string { return []string{Prevented, Incorrect, Unresolved} }

// Receipt is one adjudicated firing.
//
// HookDigest, RuleHash and MasterBuildDigest are copied from the row that was read,
// so the receipt records which implementation it judged rather than whichever one is
// installed when it is read back.
type Receipt struct {
	FiringID string `json:"firing_id"`
	HookID   string `json:"hook_id,omitempty"`
	Harness  string `json:"harness,omitempty"`

	Judgment  string `json:"judgment"`
	Rationale string `json:"rationale,omitempty"`
	// Evidence names what was read to reach the judgment: a payload sidecar, a
	// transcript path, a commit. A judgment whose evidence nobody can open is not
	// inspectable, so this is the receipt's only support.
	Evidence []string `json:"evidence,omitempty"`

	// ContinuationInspected reports whether the task after the block was read.
	// A correct block whose continuation nobody read leaves benefit unresolved, so
	// the two facts are stored apart.
	ContinuationInspected bool   `json:"continuation_inspected"`
	ContinuationOutcome   string `json:"continuation_outcome,omitempty"`

	// Selection records why this firing was read: which rule of the sampling pass
	// picked it. A later pass advances coverage instead of repeating the same cases.
	Selection string `json:"selection,omitempty"`
	JudgedAt  string `json:"judged_at,omitempty"`

	HookDigest        string `json:"hook_digest,omitempty"`
	RuleHash          string `json:"rule_hash,omitempty"`
	MasterBuildDigest string `json:"master_build_digest,omitempty"`
}

// Dir names the component directory a reader should read. A writer calls
// statestore.Prepare instead, which migrates a legacy corpus before it writes.
func Dir() string { return statestore.Resolve(Component) }

// Load reads every receipt in a component directory, oldest line first. A missing
// stream yields no receipts and no error, so a machine that has adjudicated nothing
// reads the same as one whose store is absent.
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
		if r.FiringID == "" {
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
// A receipt naming no firing is refused: the row identifier is the only identity a
// judgment can carry, and a judgment attached to nothing validates nothing.
func Append(dir string, r Receipt) error {
	if r.FiringID == "" {
		return fmt.Errorf("a receipt must name the firing it judges")
	}
	if !validJudgment(r.Judgment) {
		return fmt.Errorf("invalid judgment %q: expected one of %s",
			r.Judgment, strings.Join(Judgments(), ", "))
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

func validJudgment(j string) bool {
	for _, known := range Judgments() {
		if j == known {
			return true
		}
	}
	return false
}

// Latest reduces a stream to one receipt per firing, keeping the last line for each.
// The stream is append-only, so a re-read of the same case supersedes the earlier
// judgment rather than replacing it in place.
func Latest(receipts []Receipt) map[string]Receipt {
	out := make(map[string]Receipt, len(receipts))
	for _, r := range receipts {
		out[r.FiringID] = r
	}
	return out
}
