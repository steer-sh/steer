package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// defaultLogLockWait bounds how long a writer waits for the shard's lock. The holder
	// keeps it only across one read or one rewrite.
	defaultLogLockWait = 5 * time.Second
	logLockPoll        = 10 * time.Millisecond
)

// ErrLogLockBusy reports that the shard's lock stayed held for the whole wait.
var ErrLogLockBusy = errors.New("ledger lock busy")

// TryLockFile takes an exclusive OS lock on the file at path without waiting. held is
// false when another open file has it. Keep the file after release: removing it permits
// two writers to lock different inodes. The OS releases the lock when a holder exits.
func TryLockFile(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	held, err = tryLockFile(f)
	if err != nil || !held {
		f.Close()
		return nil, false, err
	}
	return func() {
		unlockFile(f)
		f.Close()
	}, true, nil
}

// LockLog holds the lock every writer of the shard shares, waiting at most
// cfg.LogLockWait (zero means the default). A rewrite holds it from reading the shard
// through the rename, so no append lands on the inode the rename unlinks.
func LockLog(cfg Config) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile()), 0o755); err != nil {
		return nil, err
	}
	wait := cfg.LogLockWait
	if wait == 0 {
		wait = defaultLogLockWait
	}
	deadline := time.Now().Add(wait)
	for {
		release, held, err := TryLockFile(cfg.LogFile() + ".lock")
		if err != nil {
			return nil, err
		}
		if held {
			return release, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w after %s", ErrLogLockBusy, wait)
		}
		time.Sleep(logLockPoll)
	}
}
