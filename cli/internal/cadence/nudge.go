package cadence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StaleAfter is the age past which the cadence entrypoint spawns a background
// refresh. A refresh takes seconds and spends no model tokens, so a weekly cycle
// keeps the queue current without nagging.
const StaleAfter = 7 * 24 * time.Hour

// NudgePath holds the last-nudged record, so a still-pending queue nudges once per
// change and once per refresh cycle, never every session.
func NudgePath(dir string) string { return filepath.Join(dir, "last_nudge") }

// nudgeRecord remembers what the last nudge was about: the queue hash it named and
// the refresh cycle it fired on. A changed hash re-nudges; a newer cycle re-nudges
// a still-pending queue, so an unseen nudge is not buried forever.
type nudgeRecord struct {
	Hash    string `json:"hash"`
	Refresh string `json:"refresh"`
}

// QueueHash is the change key of the queue file: the sha256 of its bytes. The queue
// is byte-identical across refreshes over identical evidence, so an unchanged hash
// means no new work, and the nudge stays silent.
func QueueHash(queueBytes []byte) string {
	sum := sha256.Sum256(queueBytes)
	return hex.EncodeToString(sum[:])
}

// PendingCounts returns the two numbers the nudge names: candidates needing a rule
// and interventions scored as needing attention.
func PendingCounts(q *Queue) (candidates, ineffective int) {
	return len(q.Candidates), len(q.Interventions)
}

// hasPending reports whether the queue holds work worth a nudge. Unscored rows and
// the corpus sweep are informational — only candidates and flagged interventions
// are actionable, so a pure sweep never nudges.
func hasPending(q *Queue) bool {
	return len(q.Candidates) > 0 || len(q.Interventions) > 0
}

// ShouldNudge decides whether to emit the nudge and returns the record to persist.
// It re-emits when the queue hash changed OR a newer refresh cycle ran since the
// last nudge — weekly re-surface for a still-pending queue, no per-session nag.
func ShouldNudge(dir string, q *Queue, queueHash string, refresh time.Time) (bool, nudgeRecord) {
	rec := nudgeRecord{Hash: queueHash, Refresh: refresh.UTC().Format(time.RFC3339)}
	if !hasPending(q) {
		return false, rec
	}
	prev := readNudge(dir)
	if prev.Hash != queueHash {
		return true, rec
	}
	prevRefresh, err := time.Parse(time.RFC3339, prev.Refresh)
	if err != nil {
		return true, rec
	}
	// The persisted record is RFC3339 (second precision), so compare at that
	// resolution — otherwise sub-second jitter reads as a new cycle every session.
	return refresh.UTC().Truncate(time.Second).After(prevRefresh), rec
}

// WriteNudge persists the last-nudged record. A write failure is non-fatal: the
// worst case is one repeated nudge, never a blocked session.
func WriteNudge(dir string, rec nudgeRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeAtomic(NudgePath(dir), append(data, '\n'))
}

// readNudge reads the last-nudged record. A missing or unparsable file reads as the
// zero record, which forces a nudge on the next pending queue.
func readNudge(dir string) nudgeRecord {
	data, err := os.ReadFile(NudgePath(dir))
	if err != nil {
		return nudgeRecord{}
	}
	var rec nudgeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nudgeRecord{}
	}
	return rec
}
