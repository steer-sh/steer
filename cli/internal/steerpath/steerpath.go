// Package steerpath resolves the directories Steer owns. It is the single place
// that knows where evidence and guards live, so every corpus moves together or
// not at all.
package steerpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// steerHome is the neutral root holding everything Steer owns: the guard store
// and the evidence corpora. Both are harness-neutral — one master, one audit
// stream and one adjudication ledger serve every harness — so neither can sit
// inside one harness's config directory.
const steerHome = ".steer"

// configFileName is the global, harness-neutral settings file inside steerHome.
const configFileName = "config.json"

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

// ConfigFile returns the global Steer settings file. When the home directory
// cannot be resolved, it returns the same path relative to the working directory.
func ConfigFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(steerHome, configFileName)
	}
	return filepath.Join(home, steerHome, configFileName)
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

// projectRoots caches a resolved repository root by the directory it was
// resolved from. Only a success is cached: a directory that is not a repository
// yet can become one inside one process, and a check-tier command creates
// repositories.
var projectRoots sync.Map

// PhysicalDir returns dir as an absolute path with its symlinks resolved, and
// the working directory for "". A caller comparing a path to a git answer needs
// this form, because git reports the physical path.
func PhysicalDir(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// ProjectRoot returns the directory project scope resolves against for the
// working directory. See ProjectRootOf.
func ProjectRoot() (string, error) { return ProjectRootOf("") }

// ProjectRootOf returns the directory project scope resolves against for dir,
// which is "" for the working directory: the repository containing dir, or dir
// itself when it is in none. The answer is git's own, in the physical form
// PhysicalDir returns, and it is an anchor for filesystem access rather than an
// identity to compare against a stored root.
func ProjectRootOf(dir string) (string, error) {
	abs, err := PhysicalDir(dir)
	if err != nil {
		return "", err
	}
	if root, ok := projectRoots.Load(abs); ok {
		return root.(string), nil
	}
	out, err := exec.Command("git", "-C", abs, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return abs, nil
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return abs, nil
	}
	projectRoots.Store(abs, root)
	return root, nil
}

// LinkBound returns the directory a project file Steer writes must resolve
// inside under scope. Under project scope it is the working directory's project
// root, since the repository carries any link on the way to the file. Under
// global scope it is "": the operator made the link.
func LinkBound(scope string) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(scope), ProjectScope) {
		return "", nil
	}
	return ProjectRoot()
}

// Within reports whether path, with its symlinks resolved, lies inside dir. A
// path that does not exist yet is placed by its directory, so a linked parent
// still counts; a link that does not resolve lies nowhere and is outside.
func Within(dir, path string) bool {
	root, err := PhysicalDir(dir)
	if err != nil {
		return false
	}
	target, err := physicalPath(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// physicalPath returns path absolute with its symlinks resolved. A path that
// does not exist keeps its own name under its directory's physical path. A link
// that does not resolve is an error, since a write through it creates its target.
func physicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if _, lerr := os.Lstat(abs); !os.IsNotExist(lerr) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}
	dir, err := physicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.Base(abs)), nil
}

// HooksDir returns the neutral guard directory for a scope, for the working
// directory's repository. See HooksDirIn.
func HooksDir(scope string) (string, error) { return HooksDirIn(scope, "") }

// HooksDirIn returns the neutral guard directory of the repository holding dir,
// which is "" for the working directory. It is a filesystem path; the path a
// registration names is hookstore.MasterRegistration, and the path a git object
// read uses is ProjectHooksRel.
func HooksDirIn(scope, dir string) (string, error) {
	return scopedDir(scope, steerHome, dir)
}

// ProjectHooksRel returns the guard store's path relative to the repository root,
// neutral store first and legacy second. A git object path is repository-relative
// by definition, so a reader of a committed store uses this rather than HooksDir.
func ProjectHooksRel() []string {
	return []string{
		filepath.Join(steerHome, hooksDirName),
		filepath.Join(legacyHome, hooksDirName),
	}
}

// LegacyHooksDir returns the guard directory Steer wrote before the store moved.
func LegacyHooksDir(scope string) (string, error) { return LegacyHooksDirIn(scope, "") }

// LegacyHooksDirIn is LegacyHooksDir for the repository holding dir.
func LegacyHooksDirIn(scope, dir string) (string, error) {
	return scopedDir(scope, legacyHome, dir)
}

func scopedDir(scope, root, dir string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ProjectScope:
		project, err := ProjectRootOf(dir)
		if err != nil {
			return "", err
		}
		return filepath.Join(project, root, hooksDirName), nil
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

// checksDirName is the repository check store inside a repository's steerHome.
const checksDirName = "checks"

// ChecksDir returns the check store of the repository rooted at root. Checks
// belong to one repository, so the store has no global scope.
func ChecksDir(root string) string {
	return filepath.Join(root, steerHome, checksDirName)
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
