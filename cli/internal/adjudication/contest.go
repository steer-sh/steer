package adjudication

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/jsonl"
)

// ContestLogName is the append-only stream of contests, beside the receipts in the
// same component directory.
const ContestLogName = "contests.jsonl"

// MaxContestReason bounds the reason a contest carries, in bytes.
const MaxContestReason = 2000

// Contest is a blocked agent's statement that one block was wrong for its task.
//
// It is a complaint, not a judgment: no receipt reader counts it as a reading, and
// it never changes what a guard or check.sh enforces. The agent supplies the
// block's identifier and Reason: FiringID for a guard subject, RunID for a check
// subject. The record fields are copied from the audit row or run the identifier
// names, and Matched is false when none carried it, in which case they are empty.
type Contest struct {
	Subject    string `json:"subject,omitempty"`
	FiringID   string `json:"firing_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Reason     string `json:"reason"`
	RecordedAt string `json:"recorded_at"`
	// Cwd is the working directory the contest command ran in.
	Cwd string `json:"cwd,omitempty"`

	Matched   bool   `json:"matched"`
	HookID    string `json:"hook_id,omitempty"`
	Harness   string `json:"harness,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	StoreDir  string `json:"store_dir,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`

	HookDigest string `json:"hook_digest,omitempty"`
	RuleHash   string `json:"rule_hash,omitempty"`
}

// ID is the identifier the contest's subject is named by, or "" for a subject
// this build does not know.
func (c Contest) ID() string {
	return Receipt{Subject: c.Subject, FiringID: c.FiringID, RunID: c.RunID}.ID()
}

// ValidRunID reports whether id has a form a check run identifier takes:
// lowercase hexadecimal, or the decimal a failed random source falls back to.
func ValidRunID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// ValidFiringID reports whether id has the form the runtime writes: 32 lowercase
// hexadecimal characters.
func ValidFiringID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// LoadContests reads every contest in a component directory, oldest first. A
// missing stream yields none and no error. A line naming no subject reads as a
// guard contest; a line whose subject this build does not know is skipped.
func LoadContests(dir string) ([]Contest, error) {
	file, err := os.Open(filepath.Join(dir, ContestLogName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", ContestLogName, err)
	}
	defer file.Close()
	var out []Contest
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Contest
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		if c.Subject == "" {
			c.Subject = SubjectGuard
		}
		if c.ID() == "" {
			continue
		}
		out = append(out, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", ContestLogName, err)
	}
	return out, nil
}

// AppendContest writes one contest to the stream in dir, which must already exist.
// It refuses an unknown subject, a malformed identifier and an empty or
// oversized reason.
func AppendContest(dir string, c Contest) error {
	switch c.Subject {
	case SubjectGuard:
		if !ValidFiringID(c.FiringID) || c.RunID != "" {
			return fmt.Errorf("invalid firing identifier %q: expected the 32 hexadecimal characters the block printed", c.FiringID)
		}
	case SubjectCheck:
		if !ValidRunID(c.RunID) || c.FiringID != "" {
			return fmt.Errorf("invalid run identifier %q: expected the identifier after \"steer check run:\" in the block message", c.RunID)
		}
	default:
		return fmt.Errorf("invalid subject %q: expected %s or %s", c.Subject, SubjectGuard, SubjectCheck)
	}
	if strings.TrimSpace(c.Reason) == "" {
		return fmt.Errorf("a contest must state why the block was wrong for this task")
	}
	if len(c.Reason) > MaxContestReason {
		return fmt.Errorf("the reason is %d bytes; keep it under %d", len(c.Reason), MaxContestReason)
	}
	line, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to encode the contest: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, ContestLogName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", ContestLogName, err)
	}
	defer file.Close()
	if _, err := file.Write(jsonl.Separated(filepath.Join(dir, ContestLogName), append(line, '\n'))); err != nil {
		return fmt.Errorf("failed to append to %s: %w", ContestLogName, err)
	}
	return nil
}

// Contested reduces a stream to the distinct blocks of one subject it names,
// keyed by identifier, each with every contest submitted for it in order.
func Contested(entries []Contest, subject string) map[string][]Contest {
	out := map[string][]Contest{}
	for _, c := range entries {
		if c.Subject == subject && c.ID() != "" {
			out[c.ID()] = append(out[c.ID()], c)
		}
	}
	return out
}
