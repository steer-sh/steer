// Package steerpath resolves the directories Steer owns. It is the single place
// that knows where evidence and guards live, so every corpus moves together or
// not at all.
package steerpath

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// steerHome is the neutral root holding everything Steer owns: the guard store
// and the evidence corpora. Both are harness-neutral — one master, one audit
// stream and one adjudication ledger serve every harness — so neither can sit
// inside one harness's config directory.
const steerHome = ".steer"

// legacyHome is where the guard store and the corpora lived while Claude Code
// was the only harness.
const legacyHome = ".claude"

// hooksDirName is the guard directory inside either root.
const hooksDirName = "hooks"

// stateDirName is the evidence directory inside either root.
const stateDirName = "state"

const (
	ProjectScope = "project"
	GlobalScope  = "global"
)

// StateDir returns the neutral directory holding one component's state, such as
// "commit-review" or "steer-report". When the home directory cannot be
// resolved, it returns the same path relative to the working directory.
func StateDir(component string) string {
	return stateDir(steerHome, component)
}

// LegacyStateDir returns the directory Steer wrote a component's state to
// before the evidence root moved out of the Claude Code home.
func LegacyStateDir(component string) string {
	return stateDir(legacyHome, component)
}

func stateDir(root, component string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(root, stateDirName, component)
	}
	return filepath.Join(home, root, stateDirName, component)
}

// HooksDir returns the neutral guard directory for a scope. Project scope is
// relative to the working directory, matching how a project registration names
// its master hook.
func HooksDir(scope string) (string, error) {
	return scopedDir(scope, steerHome)
}

// LegacyHooksDir returns the guard directory Steer wrote before the store moved.
func LegacyHooksDir(scope string) (string, error) {
	return scopedDir(scope, legacyHome)
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

// binDirName holds the stable copy of the steer executable inside the neutral
// root. A hook registration names that path, so it must not move when the
// binary a user runs moves.
const binDirName = "bin"

// BinStem is the executable's name without an extension. It is what identifies a
// command as steer's own, on every platform.
const BinStem = "steer"

// BinName is the file the stable executable is written as. Windows runs a file by
// its extension, so the stable copy carries .exe there or the registration names
// a file Windows will not execute.
var BinName = binName()

func binName() string {
	if runtime.GOOS == "windows" {
		return BinStem + ".exe"
	}
	return BinStem
}

// BinDir returns the directory holding the stable executable.
func BinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, steerHome, binDirName), nil
}

// BinPath returns the stable executable's absolute path.
func BinPath() (string, error) {
	dir, err := BinDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, BinName), nil
}
