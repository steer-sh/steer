// Package firingaudit reads the guard audit stream whole, with the evidence each
// row recorded. mine.LoadFirings reads the same stream to count it and keeps only
// the identifying stamps; judging a block needs the attempted action itself, so
// this package also resolves the bundle a row holds inline or names in a sidecar,
// and reports what it could not establish instead of returning a partial bundle as
// though it were whole.
package firingaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AuditLogName is the guard store's audit stream, one JSON row per block decision.
const AuditLogName = "steer-audit.jsonl"

// PayloadsDir is the directory inside a guard store holding the bundles too large
// to sit in a row. A row names its own file within it.
const PayloadsDir = "steer-audit-payloads"

// Row is one recorded block, read with every field the master wrote.
//
// The stamps and the locators are absent on a row an older master wrote, so a
// reader tests presence rather than assuming any of them arrived.
type Row struct {
	Timestamp   string `json:"timestamp"`
	FiringID    string `json:"firing_id"`
	Rule        string `json:"rule"`
	HookName    string `json:"hook_name"`
	Enforcement string `json:"enforcement"`
	Harness     string `json:"harness"`
	SessionID   string `json:"session_id"`
	ThreadID    string `json:"thread_id"`
	Repository  string `json:"repository"`
	FilePath    string `json:"file_path"`
	ToolName    string `json:"tool_name"`
	// Snippet is a truncated form of the blocked input, kept for a reader that has
	// no bundle. The bundle holds the input whole; prefer it where it resolves.
	Snippet string `json:"snippet"`

	HookDigest        string `json:"hook_digest"`
	RuleHash          string `json:"rule_hash"`
	MasterBuildDigest string `json:"master_build_digest"`

	// Locators are the call identifiers the harness supplied, by key presence, so
	// an absent key and an empty one are different facts.
	Locators map[string]string `json:"locators"`

	// Evidence is the bundle when the row carries it inline, left encoded so the
	// bytes a reader inspects are the bytes the master wrote.
	Evidence json.RawMessage `json:"evidence"`
	// EvidenceRef names the sidecar, relative to the store directory, when the
	// bundle was too large for the row.
	EvidenceRef string `json:"evidence_ref"`
	// EvidenceBytes and EvidenceSHA256 describe the encoded bundle. They are the
	// only check on a sidecar's contents.
	EvidenceBytes  int    `json:"evidence_bytes"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	// EvidenceComplete reports that the master retained the bundle. EvidenceError
	// names which step failed when it did not: "encode" or "write".
	EvidenceComplete bool   `json:"evidence_complete"`
	EvidenceError    string `json:"evidence_error"`
}

// Where a resolved bundle came from, and what that says about it.
const (
	// Inline means the bundle came from the row. The row is the only copy, so its
	// length and digest describe what the master encoded and nothing rechecks them.
	Inline = "inline"
	// Sidecar means the bundle came from the file the row names, and the file's
	// bytes matched both stamps the row recorded.
	Sidecar = "sidecar"
	// Unretained means the master tried to keep the bundle and failed, and named
	// the step that failed.
	Unretained = "unretained"
	// Unrecorded means the master that wrote the row captured no bundle at all.
	// Rows written before evidence capture read this, and that set does not grow.
	Unrecorded = "unrecorded"
	// Damaged means the row names a sidecar this reader could not stand behind.
	Damaged = "damaged"
)

// Evidence is one row's bundle: the payload as it arrived beside the virtual edit
// the guard objected to. Bundle is empty unless Source is Inline or Sidecar.
type Evidence struct {
	Source string
	Bundle json.RawMessage
	// Reason says why there is no bundle, when there is none.
	Reason string
}

// Load reads the audit streams in the named store directories.
//
// The bool reports whether any stream was readable, which separates "the guard
// never fired" from "no audit evidence exists here". Rows are deduplicated on the
// same key mine.LoadFirings uses, so both readers agree on what one row is.
func Load(dirs ...string) (rows []Row, present bool) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, AuditLogName))
		if err != nil {
			continue
		}
		present = true
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row Row
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				continue
			}
			key := row.FiringID
			if key == "" {
				key = row.HookName + "\x1f" + row.Harness + "\x1f" +
					row.SessionID + "\x1f" + row.Timestamp
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, row)
		}
	}
	return rows, present
}

// Find returns the row carrying an identifier, and whether one did.
//
// A row with no identifier is never returned: the identifier is the only field
// that names one row, so a judgment can attach to nothing else.
func Find(rows []Row, firingID string) (Row, bool) {
	if firingID == "" {
		return Row{}, false
	}
	for _, r := range rows {
		if r.FiringID == firingID {
			return r, true
		}
	}
	return Row{}, false
}

// Resolve returns the bundle a row recorded, reading storeDir for a sidecar.
//
// A sidecar is returned only when its bytes match the length and digest the row
// recorded; any other outcome is Damaged, because an unchecked bundle would be
// evidence of nothing in particular.
func (r Row) Resolve(storeDir string) Evidence {
	if r.EvidenceRef != "" {
		return r.resolveSidecar(storeDir)
	}
	if hasBundle(r.Evidence) {
		return Evidence{Source: Inline, Bundle: r.Evidence}
	}
	if r.EvidenceError == "" {
		return Evidence{Source: Unrecorded,
			Reason: "the master that wrote this row captured no evidence"}
	}
	return Evidence{Source: Unretained, Reason: r.unretainedReason()}
}

func (r Row) resolveSidecar(storeDir string) Evidence {
	path, err := sidecarPath(storeDir, r.EvidenceRef)
	if err != nil {
		return Evidence{Source: Damaged, Reason: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Evidence{Source: Damaged,
			Reason: fmt.Sprintf("the sidecar %s did not open: %v", r.EvidenceRef, err)}
	}
	if r.EvidenceSHA256 == "" || r.EvidenceBytes <= 0 {
		return Evidence{Source: Damaged, Reason: fmt.Sprintf(
			"the row names the sidecar %s and recorded no length and digest to check it against",
			r.EvidenceRef)}
	}
	if len(data) != r.EvidenceBytes {
		return Evidence{Source: Damaged, Reason: fmt.Sprintf(
			"the sidecar %s holds %d bytes and the row recorded %d",
			r.EvidenceRef, len(data), r.EvidenceBytes)}
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != r.EvidenceSHA256 {
		return Evidence{Source: Damaged, Reason: fmt.Sprintf(
			"the sidecar %s digests to %s and the row recorded %s",
			r.EvidenceRef, got, r.EvidenceSHA256)}
	}
	return Evidence{Source: Sidecar, Bundle: data}
}

// hasBundle reports whether an encoded field holds something to read. A row can
// carry the JSON null, which decodes to four bytes and is not a bundle.
func hasBundle(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// sidecarPath resolves a row's reference inside the store. A reference that
// escapes the store directory is refused rather than read.
func sidecarPath(storeDir, ref string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(ref))
	if filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the evidence reference %q leaves the guard store", ref)
	}
	return filepath.Join(storeDir, clean), nil
}

// unretainedReason states which step of the write failed, in the master's terms.
func (r Row) unretainedReason() string {
	switch r.EvidenceError {
	case "encode":
		return "the master could not encode the bundle"
	case "write":
		return "the master could not write the bundle to a sidecar"
	default:
		return fmt.Sprintf("the master reported %q writing the bundle", r.EvidenceError)
	}
}
