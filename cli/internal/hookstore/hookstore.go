// Package hookstore resolves the guard store on disk and moves a legacy store
// into the neutral root. Guards are shared between harnesses, so the store sits
// under .steer rather than inside either harness's config directory.
//
// A reader calls Resolve and gets whichever store holds the guards. A mutation
// calls Prepare, which copies a legacy store into the neutral root first. The
// copy never deletes: the legacy files stay where they are, so an older steer
// binary still finds them.
package hookstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/shellword"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const (
	MasterName   = "steer-hooks.py"
	ManifestName = "steer-manifest.json"
	AuditName    = "steer-audit.jsonl"
	PayloadsName = "steer-audit-payloads"
	IgnoreName   = ".gitignore"
)

// Dir returns the neutral guard directory for a scope, whether or not it exists.
func Dir(scope string) (string, error) { return DirIn(scope, "") }

// DirIn is Dir for the repository holding dir, which is "" for the working
// directory.
func DirIn(scope, dir string) (string, error) { return steerpath.HooksDirIn(scope, dir) }

// LegacyDir returns the guard directory Steer wrote before the store moved.
func LegacyDir(scope string) (string, error) { return LegacyDirIn(scope, "") }

// LegacyDirIn is LegacyDir for the repository holding dir.
func LegacyDirIn(scope, dir string) (string, error) {
	return steerpath.LegacyHooksDirIn(scope, dir)
}

// MasterPath names the generated master hook inside the resolved store. It is a
// filesystem path, absolute under project scope; MasterRegistration renders the
// form a registration names.
func MasterPath(scope string) (string, error) { return MasterPathIn(scope, "") }

// MasterPathIn is MasterPath for the repository holding dir.
func MasterPathIn(scope, dir string) (string, error) {
	store, err := ResolveIn(scope, dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(store, MasterName), nil
}

// MasterRegistration names the master as a registration must name it: under
// project scope a path relative to configDir, the directory holding the harness
// config that carries it, and under global scope the absolute path. configDir is
// "" for the working directory.
func MasterRegistration(scope, configDir string) (string, error) {
	path, err := MasterPathIn(scope, configDir)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(scope), steerpath.ProjectScope) {
		return path, nil
	}
	// The store was resolved for configDir's own repository, so both sides are
	// the physical path and Rel finds the common parent.
	anchor, err := steerpath.PhysicalDir(configDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(anchor, path)
	if err != nil {
		return path, nil
	}
	return rel, nil
}

// IsMasterCommand reports whether a registered hook command runs Steer's master.
// Both harnesses' registrations are recognized by the one file they invoke.
func IsMasterCommand(command string) bool { return strings.Contains(command, MasterName) }

// Resolve names the directory a reader should read. It is the legacy directory
// only while that one holds a store and the neutral one does not, so a machine
// that has never migrated keeps working and a migrated machine never reads back.
func Resolve(scope string) (string, error) { return ResolveIn(scope, "") }

// ResolveIn is Resolve for the repository holding dir, which is "" for the
// working directory.
func ResolveIn(scope, dir string) (string, error) {
	neutral, err := DirIn(scope, dir)
	if err != nil {
		return "", err
	}
	if populated(neutral) {
		return neutral, nil
	}
	legacy, err := LegacyDirIn(scope, dir)
	if err != nil {
		return "", err
	}
	if populated(legacy) {
		return legacy, nil
	}
	return neutral, nil
}

// Prepare names the directory a mutation must write to, creating it and copying
// a legacy store into it first. The copy is additive: a file already in the
// neutral store is kept and its legacy counterpart is left where it is.
func Prepare(scope string) (string, error) {
	neutral, err := Dir(scope)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(neutral, 0o755); err != nil {
		return "", fmt.Errorf("failed to create hooks directory: %w", err)
	}
	if err := ignoreEvidence(neutral); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", IgnoreName, err)
	}
	legacy, err := LegacyDir(scope)
	if err != nil {
		return "", err
	}
	if legacy == neutral || populated(neutral) || !populated(legacy) {
		return neutral, nil
	}
	if err := migrate(legacy, neutral); err != nil {
		return "", err
	}
	return neutral, nil
}

// ignoreEvidence makes sure the store's .gitignore names both evidence paths:
// the audit log and the directory holding the payloads too large to sit inline
// in a row. A project store is committed with its manifest, while the evidence
// holds this machine's blocked commands, sessions and tool inputs. An existing
// .gitignore keeps its lines and gains only the entries no line already names.
func ignoreEvidence(dir string) error {
	path := filepath.Join(dir, IgnoreName)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	named := make(map[string]bool, 2)
	for _, line := range strings.Split(string(raw), "\n") {
		entry := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "/"), "/")
		named[entry] = true
	}
	missing := make([]string, 0, 2)
	for _, entry := range []string{AuditName, PayloadsName + "/"} {
		if !named[strings.TrimSuffix(entry, "/")] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	content := string(raw)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeWhole(path, []byte(content+strings.Join(missing, "\n")+"\n"), 0o644)
}

// populated reports whether a directory holds a steer store. The manifest is the
// inventory, so its absence means there is nothing to read or move.
func populated(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
		return true
	}
	return false
}

// ownedFiles lists the store's own files inside dir. A hooks directory can hold
// another tool's hooks, so the manifest names what belongs to steer and nothing
// else is copied.
func ownedFiles(dir string) ([]string, error) {
	names := []string{ManifestName, MasterName, AuditName}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", ManifestName, err)
	}
	var manifest struct {
		Hooks map[string]struct {
			File string `json:"file"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", ManifestName, err)
	}
	for name, entry := range manifest.Hooks {
		// Migration runs before any reader validates the manifest, and it copies
		// what these names address. A basename repair would move a file the
		// manifest never named, so a malformed name stops the migration instead.
		file, err := EntryFileName(filepath.Join(dir, ManifestName), name, entry.File)
		if err != nil {
			return nil, err
		}
		names = append(names, file)
	}
	return names, nil
}

// migrate copies the legacy store into the neutral one. It never overwrites a
// file already in the neutral store: the store is also where an author writes a
// new guard before registering it, so the neutral copy is the newer one and the
// legacy file stays readable where it is.
//
// Every file is written through a temporary name and renamed into place, so an
// interrupted migration leaves whole files or none. The manifest lands last, in
// a fixed order after the rest, because the manifest is what marks the store
// migrated: a write that fails partway leaves the store unmarked, and the next
// Prepare copies whatever is still missing.
func migrate(legacy, neutral string) error {
	names, err := ownedFiles(legacy)
	if err != nil {
		return err
	}
	staged := map[string][]byte{}
	for _, name := range names {
		if _, ok := staged[name]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(neutral, name)); err == nil {
			continue
		}
		content, err := os.ReadFile(filepath.Join(legacy, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", name, err)
		}
		staged[name] = content
	}
	order := make([]string, 0, len(staged))
	for name := range staged {
		if name != ManifestName {
			order = append(order, name)
		}
	}
	sort.Strings(order)
	if _, ok := staged[ManifestName]; ok {
		order = append(order, ManifestName)
	}
	for _, name := range order {
		mode := os.FileMode(0o644)
		if info, err := os.Stat(filepath.Join(legacy, name)); err == nil {
			mode = info.Mode().Perm()
		}
		if err := writeWhole(filepath.Join(neutral, name), staged[name], mode); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}
	return nil
}

// writeWhole writes content to path through a temporary file in the same
// directory, so a reader never sees a partly written file.
func writeWhole(path string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".steer-migrate.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// MasterPathFromCommand reads the master's path out of a registered hook command
// (`python3 <path> [flags]`). A registration is the only record of which master a
// harness actually runs, and on a migrated machine that is not always the one
// Resolve answers: the legacy files are left in place for older binaries, so both
// paths can hold a master. The result is "" when the command runs no master.
//
// The path is the interpreter's first argument, decoded, so a path holding a
// space names one file here as it does to the shell. A command the codec cannot
// read yields "": the alternative is a fragment of a path, which a caller cannot
// tell from a short one.
func MasterPathFromCommand(command string) string {
	_, args, ok := shellword.Split(command)
	if !ok {
		return ""
	}
	path, _, ok := shellword.Split(args)
	if !ok || !strings.HasSuffix(path, MasterName) {
		return ""
	}
	return path
}
