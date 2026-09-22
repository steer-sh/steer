package adjudication

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContestLogName is the append-only stream of contests, beside the receipts in the
// same component directory.
const ContestLogName = "contests.jsonl"

// MaxContestReason bounds the reason a contest carries, in bytes.
const MaxContestReason = 2000

// Contest is a blocked agent's statement that one block was wrong for its task.
//
// It is a complaint, not a judgment: no receipt reader counts it as a reading, and
// it never changes what a guard enforces. The agent supplies FiringID and Reason.
// The row fields are copied from the audit row the identifier names, and Matched
// is false when no row carried it, in which case they are empty.
type Contest struct {
	FiringID   string `json:"firing_id"`
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
// missing stream yields none and no error.
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
		if err := json.Unmarshal([]byte(line), &c); err != nil || c.FiringID == "" {
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
// It refuses a malformed firing identifier and an empty or oversized reason.
func AppendContest(dir string, c Contest) error {
	if !ValidFiringID(c.FiringID) {
		return fmt.Errorf("invalid firing identifier %q: expected the 32 hexadecimal characters the block printed", c.FiringID)
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
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("failed to append to %s: %w", ContestLogName, err)
	}
	return nil
}

// Contested reduces a stream to the distinct firings it names, keyed by firing
// identifier, each with every contest submitted for it in order.
func Contested(entries []Contest) map[string][]Contest {
	out := map[string][]Contest{}
	for _, c := range entries {
		out[c.FiringID] = append(out[c.FiringID], c)
	}
	return out
}
