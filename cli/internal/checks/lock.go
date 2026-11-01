package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
)

const lockName = "oracle.lock"

// ErrConcurrentRun reports an oracle run refused because another run holds
// the repository.
var ErrConcurrentRun = errors.New("another " + OracleName + " run holds the repository")

// LockHolder names the oracle run that holds a repository's lock.
type LockHolder struct {
	RunID   string    `json:"run_id"`
	PID     int       `json:"pid"`
	Source  string    `json:"source"`
	Started time.Time `json:"started"`
}

// repoLock is an exclusive per-repository lock held through an open file. The
// operating system releases it when the holder exits, so a killed run never
// leaves the repository locked.
type repoLock struct{ f *os.File }

// acquireRepoLock takes the lock for identity without waiting. When another
// run holds it, the error wraps ErrConcurrentRun and the holder is returned as
// far as it could be read.
func acquireRepoLock(identity string, holder LockHolder) (*repoLock, LockHolder, error) {
	dir, err := statestore.Prepare(StateComponent)
	if err != nil {
		return nil, LockHolder{}, err
	}
	path := filepath.Join(dir, repoKey(identity), lockName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, LockHolder{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, LockHolder{}, err
	}
	locked, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, LockHolder{}, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		f.Close()
		var other LockHolder
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &other)
		}
		return nil, other, ErrConcurrentRun
	}
	if data, err := json.Marshal(holder); err == nil {
		_ = f.Truncate(0)
		_, _ = f.WriteAt(data, 0)
	}
	return &repoLock{f: f}, holder, nil
}

func (l *repoLock) release() {
	unlock(l.f)
	l.f.Close()
}
