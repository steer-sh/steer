// Package steerpath resolves the directories Steer owns. It is the single place
// that knows where evidence lives, so every corpus moves together or not at all.
package steerpath

import (
	"os"
	"path/filepath"
)

// evidenceHome is the directory the state root sits under. Steer's corpora are
// harness-neutral, but the shards already on disk live under the Claude Code
// home, and a reader that cannot find them reports an empty corpus rather than
// an error.
const evidenceHome = ".claude"

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
