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
	// swapLockWait bounds the wait for LockSwap. A rewrite holds it across a rename, a
	// read and an append, and a Log across a read of the shard and an append, neither
	// across a whole rewrite; a holder stalled past it still costs a rewrite its run or
	// a Log a second copy of its row.
	swapLockWait = 5 * time.Second
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
	wait := cfg.LogLockWait
	if wait == 0 {
		wait = defaultLogLockWait
	}
	return lockWithin(cfg.LogFile()+".lock", wait)
}

// LockSwap holds the lock a rewrite takes, inside LockLog, from its rename through its
// carry of the replaced shard's growth, and nowhere else. A Log whose append landed on
// a replaced shard checks the current one under it, so the check never falls between a
// rename and its carry.
func LockSwap(cfg Config) (func(), error) {
	return lockWithin(cfg.LogFile()+".swap.lock", swapLockWait)
}

func lockWithin(path string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		release, held, err := TryLockFile(path)
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
