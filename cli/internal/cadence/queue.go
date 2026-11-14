package cadence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
)

// stateSubdir is the machine-local, partial-by-design output directory.
const stateSubdir = "steer-report"

// lockStaleAfter steals a lock older than this: a refresh takes seconds, so a
// lock this old belongs to a crashed writer, not a live one.
const lockStaleAfter = 5 * time.Minute

// StateDir is where the queue and its timestamp live.
func StateDir() string { return statestore.Resolve(stateSubdir) }

// PrepareStateDir names the directory a write to this state must use, migrating
// a legacy corpus into it first.
func PrepareStateDir() (string, error) { return statestore.Prepare(stateSubdir) }

// QueuePath is the queue file the skill reads.
func QueuePath(dir string) string { return filepath.Join(dir, "queue.json") }

// TimestampPath holds the RFC3339 time of the last refresh, kept out of the queue
// so the queue stays byte-identical across refreshes over identical evidence.
func TimestampPath(dir string) string { return filepath.Join(dir, "last_refresh") }

func lockPath(dir string) string { return filepath.Join(dir, ".refresh.lock") }

// WriteQueue writes the queue and the timestamp atomically under a single-writer
// lock, so a background refresh and a skill-triggered refresh never clobber each
// other. The queue is written first, then the timestamp, both via temp + rename.
func WriteQueue(dir string, q *Queue, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	release, err := acquireLock(dir)
	if err != nil {
		return err
	}
	defer release()

	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(QueuePath(dir), append(data, '\n')); err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339) + "\n"
	if err := writeAtomic(TimestampPath(dir), []byte(stamp)); err != nil {
		return err
	}
	// The queue is already written; a stale read mark only costs a quiet status
	// line, so it never fails the refresh.
	_ = pruneReadMarks(dir, q)
	return nil
}

// LastRefresh reads the timestamp of the last successful refresh. A missing or
// unparsable file reads as the zero time, which every staleness check treats as
// "long ago" — so a machine that never refreshed always refreshes.
func LastRefresh(dir string) time.Time {
	data, err := os.ReadFile(TimestampPath(dir))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, string(trimSpace(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// ReadQueue reads the queue file. A missing file is not an error: it reads as an
// empty queue, because state here is partial by design and absence never means
// "the loop never ran" — only "this machine has nothing pending".
func ReadQueue(dir string) (*Queue, error) {
	data, err := os.ReadFile(QueuePath(dir))
	if os.IsNotExist(err) {
		return &Queue{Version: QueueVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// writeAtomic writes bytes to a temp file in the same directory, then renames it
// over the target. Rename is atomic within a directory on every supported OS.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// acquireLock takes an exclusive lock via O_CREATE|O_EXCL, portable to Windows
// where flock is unavailable. A lock older than lockStaleAfter is stolen. The
// returned func releases it.
func acquireLock(dir string) (func(), error) {
	path := lockPath(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
		return func() { os.Remove(path) }, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		// The holder released the lock between our OpenFile and this Stat, so it is
		// free now — retry rather than report a race as contention.
		return acquireLock(dir)
	}
	if statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
		os.Remove(path)
		return acquireLock(dir)
	}
	return nil, fmt.Errorf("another refresh is in progress (lock: %s)", path)
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\r' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && (b[0] == '\n' || b[0] == ' ' || b[0] == '\r' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}
