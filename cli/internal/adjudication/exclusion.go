package adjudication

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExclusionLogName is the append-only stream of corrections to the audit stream,
// beside the receipts in the same component directory.
const ExclusionLogName = "exclusions.jsonl"

// The actions an exclusion entry can take. The latest entry for a row decides.
const (
	// Exclude retires a row that records no real work, such as a firing a test
	// run caused under the operator's own guard store. The row itself stays.
	Exclude = "exclude"
	// Reinstate withdraws an earlier exclusion of the same row.
	Reinstate = "reinstate"
)

// Exclusion is one correction of one audit row.
//
// RowDigest is the identity: a row written before firing identifiers existed has
// no other. The remaining row fields are copied from the row by the caller so a
// reader can see what was retired without opening the audit stream.
type Exclusion struct {
	RowDigest string `json:"row_digest"`
	FiringID  string `json:"firing_id,omitempty"`
	HookID    string `json:"hook_id,omitempty"`
	Harness   string `json:"harness,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	StoreDir  string `json:"store_dir,omitempty"`

	Action string `json:"action"`
	Reason string `json:"reason"`
	// Evidence names what establishes the reason: a transcript, a commit, a test
	// directory. An exclude with none is refused.
	Evidence   []string `json:"evidence,omitempty"`
	RecordedAt string   `json:"recorded_at"`
}

// LoadExclusions reads every exclusion in a component directory, oldest first. A
// missing stream yields none and no error.
func LoadExclusions(dir string) ([]Exclusion, error) {
	file, err := os.Open(filepath.Join(dir, ExclusionLogName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", ExclusionLogName, err)
	}
	defer file.Close()
	var out []Exclusion
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Exclusion
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.RowDigest == "" {
			continue
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", ExclusionLogName, err)
	}
	return out, nil
}

// AppendExclusion writes one entry to the stream in dir, which must already exist.
// It refuses an entry naming no row, an unknown action, an empty reason, and an
// exclude that carries no evidence.
func AppendExclusion(dir string, e Exclusion) error {
	if e.RowDigest == "" {
		return fmt.Errorf("an exclusion must name the row it corrects")
	}
	if e.Action != Exclude && e.Action != Reinstate {
		return fmt.Errorf("invalid exclusion action %q: expected %s or %s", e.Action, Exclude, Reinstate)
	}
	if strings.TrimSpace(e.Reason) == "" {
		return fmt.Errorf("an exclusion must state its reason")
	}
	if e.Action == Exclude && len(e.Evidence) == 0 {
		return fmt.Errorf("an exclusion must name the evidence that the row records no real work")
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("failed to encode the exclusion: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, ExclusionLogName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", ExclusionLogName, err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("failed to append to %s: %w", ExclusionLogName, err)
	}
	return nil
}

// Excluded reduces a stream to the rows it currently retires, keyed by row digest.
// The latest entry for a row decides, so a reinstate after an exclude restores it.
func Excluded(entries []Exclusion) map[string]Exclusion {
	latest := map[string]Exclusion{}
	for _, e := range entries {
		latest[e.RowDigest] = e
	}
	out := map[string]Exclusion{}
	for digest, e := range latest {
		if e.Action == Exclude {
			out[digest] = e
		}
	}
	return out
}
