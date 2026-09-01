// Package steerpath resolves the directories Steer owns. It is the single place
// that knows where evidence and guards live, so every corpus moves together or
// not at all.
package steerpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// evidenceHome is the directory the state root sits under. Steer's corpora are
// harness-neutral, but the shards already on disk live under the Claude Code
// home, and a reader that cannot find them reports an empty corpus rather than
// an error.
const evidenceHome = ".claude"

// steerHome is the neutral root holding the guard store. Guards are shared
// between harnesses: one master, one manifest and one audit stream per scope,
// so the store cannot sit inside either harness's config directory.
const steerHome = ".steer"

// legacyGuardHome is where the guard store lived while Claude Code was the only
// harness.
const legacyGuardHome = ".claude"

// hooksDirName is the guard directory inside either root.
const hooksDirName = "hooks"

const (
	ProjectScope = "project"
	GlobalScope  = "global"
)

// StateDir returns the directory holding one component's state, such as
// "commit-review" or "steer-report". When the home directory cannot be
// resolved, it returns the same path relative to the working directory.
func StateDir(component string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(evidenceHome, "state", component)
	}
	return filepath.Join(home, evidenceHome, "state", component)
}

// HooksDir returns the neutral guard directory for a scope. Project scope is
// relative to the working directory, matching how a project registration names
// its master hook.
func HooksDir(scope string) (string, error) {
	return scopedDir(scope, steerHome)
}

// LegacyHooksDir returns the guard directory Steer wrote before the store moved.
func LegacyHooksDir(scope string) (string, error) {
	return scopedDir(scope, legacyGuardHome)
}

func scopedDir(scope, root string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ProjectScope:
		return filepath.Join(root, hooksDirName), nil
	case GlobalScope:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve home directory: %w", err)
		}
		return filepath.Join(home, root, hooksDirName), nil
	default:
		return "", fmt.Errorf("invalid scope %q: expected %q or %q", scope, ProjectScope, GlobalScope)
	}
}
