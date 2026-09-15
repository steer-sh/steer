package gate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChainKey identifies one gate chain: who is committing, and into which tree.
//
// Actor is the Claude Code session id under the PreToolUse adapter and HEAD under the
// pre-commit adapter, which git hands no session at all. Repo is the git toplevel, so a
// round denied from a subdirectory and retried from the root stays one chain.
type ChainKey struct {
	Actor string
	Repo  string
}

// ChainState is what one denied round leaves behind for its retry to read. The gate
// is a fresh process every invocation and has no other memory of the chain, so the
// firing count is only countable because it rides here.
type ChainState struct {
	AwaitingRetry bool   `json:"awaiting_retry"`
	TS            int64  `json:"ts"`
	FindingSlots  int    `json:"finding_slots"`
	Files         int    `json:"files"`
	Diff          string `json:"diff"`
	Firing        int    `json:"firing"`
	// Spec is the tree the denying round read: the index, or the working tree. It is
	// an observation for the ledger's spec_changed field and drives no decision — the
	// stale-index notice asks the repository what is unstaged instead, because a chain
	// that settles onto the index stops drifting while the miss stays.
	Spec DiffSpec `json:"spec"`
	// ChainID names the chain in the ledger header and on every row it writes. Empty
	// for a chain saved before IDs existed, which stays without one until it ends.
	ChainID string `json:"chain_id,omitempty"`
}

// NewChainID returns a fresh chain ID: eight hex characters, short enough for the agent
// to copy exactly. It returns "" when the system has no randomness to give, and the
// chain then joins by its subject alone.
func NewChainID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// ClearedRecord says the gate reviewed this exact diff in this repo and let it
// through, so a retry presenting the same diff is not reviewed again.
//
// Keyed on (repo, diff_sha) rather than on the actor, so either channel can read what
// the other cleared.
type ClearedRecord struct {
	DiffSHA string `json:"diff_sha"`
	Chars   int    `json:"chars"`
	TS      int64  `json:"ts"`
	Channel string `json:"channel"`
}

// Chain owns the on-disk state for one (actor, repo) pair.
type Chain struct {
	cfg Config
	key ChainKey
}

// NewChain resolves the key's repo to its git toplevel. The resolution belongs here
// rather than in the callers: `cd pkg && git commit` denies from a subdirectory and
// the retry commits from the root, and on a per-directory key those are two chains —
// the firing count resets and the cap never binds. Toplevel falls back to the resolved
// path when git cannot answer, which keeps a fixture repo the command is about to
// create in a slot of its own rather than merging it into the real chain.
func NewChain(cfg Config, key ChainKey) *Chain {
	key.Repo = Toplevel(key.Repo)
	return &Chain{cfg: cfg, key: key}
}

func repoHash(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return hex.EncodeToString(sum[:])[:12]
}

func (c *Chain) statePath() string {
	return filepath.Join(c.cfg.StateDir,
		slug(c.key.Actor, 64)+"-"+repoHash(c.key.Repo)+".json")
}

// clearedPath is a file of its own rather than a key inside the chain state: every
// chain end clears the state, and this record exists to outlive exactly that.
func (c *Chain) clearedPath(diffSHA string) string {
	return filepath.Join(c.cfg.StateDir, "cleared-"+repoHash(c.key.Repo)+"-"+diffSHA+".json")
}

// Load returns the pending chain state, or the zero value when none is pending.
func (c *Chain) Load() ChainState {
	var state ChainState
	raw, err := os.ReadFile(c.statePath())
	if err != nil {
		return ChainState{}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ChainState{}
	}
	return state
}

// Pending reports whether a denied round is still awaiting its retry.
func (c *Chain) Pending(state ChainState) bool {
	return state.AwaitingRetry &&
		time.Since(time.Unix(state.TS, 0)) < c.cfg.StaleAfter
}

// Save records the round a deny just closed. The error is returned rather than
// swallowed: losing it lets the following commit through unreviewed.
func (c *Chain) Save(state ChainState) error {
	if err := os.MkdirAll(c.cfg.StateDir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(c.statePath(), raw, 0o644)
}

// Clear ends the chain. A missing file is success.
func (c *Chain) Clear() error {
	if err := os.Remove(c.statePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RememberCleared records that this exact diff was reviewed and let through.
func (c *Chain) RememberCleared(diff, channel string) error {
	if err := os.MkdirAll(c.cfg.StateDir, 0o755); err != nil {
		return err
	}
	sha := Digest(diff)
	raw, err := json.Marshal(ClearedRecord{
		DiffSHA: sha, Chars: len(diff), TS: time.Now().Unix(), Channel: channel,
	})
	if err != nil {
		return err
	}
	c.pruneCleared()
	return os.WriteFile(c.clearedPath(sha), raw, 0o644)
}

// LoadCleared returns the unexpired cleared record for this diff, or nil.
//
// StaleAfter rather than a TTL of its own: it already carries the meaning "older than
// this is a different piece of work". The record cannot leak into a real commit
// either, because a SUCCESSFUL commit empties the staged diff and the next commit
// takes the skip-empty path.
func (c *Chain) LoadCleared(diff string) *ClearedRecord {
	raw, err := os.ReadFile(c.clearedPath(Digest(diff)))
	if err != nil {
		return nil
	}
	var record ClearedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil
	}
	if time.Since(time.Unix(record.TS, 0)) >= c.cfg.StaleAfter {
		return nil
	}
	return &record
}

// pruneCleared drops this repo's expired records. Content keying means a superseded
// record is never overwritten, so without this the state dir grows without bound.
func (c *Chain) pruneCleared() {
	entries, err := os.ReadDir(c.cfg.StateDir)
	if err != nil {
		return
	}
	prefix := "cleared-" + repoHash(c.key.Repo) + "-"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) >= c.cfg.StaleAfter {
			os.Remove(filepath.Join(c.cfg.StateDir, entry.Name()))
		}
	}
}
