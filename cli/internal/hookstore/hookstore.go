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

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const (
	MasterName   = "steer-hooks.py"
	ManifestName = "steer-manifest.json"
	AuditName    = "steer-audit.jsonl"
)

// Dir returns the neutral guard directory for a scope, whether or not it exists.
func Dir(scope string) (string, error) { return steerpath.HooksDir(scope) }

// LegacyDir returns the guard directory Steer wrote before the store moved.
func LegacyDir(scope string) (string, error) { return steerpath.LegacyHooksDir(scope) }

// MasterPath names the generated master hook inside the resolved store. Project
// scope stays relative, which is how a project registration names it.
func MasterPath(scope string) (string, error) {
	dir, err := Resolve(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, MasterName), nil
}

// IsMasterCommand reports whether a registered hook command runs Steer's master.
// Both harnesses' registrations are recognized by the one file they invoke.
func IsMasterCommand(command string) bool { return strings.Contains(command, MasterName) }

// Resolve names the directory a reader should read. It is the legacy directory
// only while that one holds a store and the neutral one does not, so a machine
// that has never migrated keeps working and a migrated machine never reads back.
func Resolve(scope string) (string, error) {
	neutral, err := Dir(scope)
	if err != nil {
		return "", err
	}
	if populated(neutral) {
		return neutral, nil
	}
	legacy, err := LegacyDir(scope)
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
		file := entry.File
		if file == "" {
			file = name + ".py"
		}
		names = append(names, filepath.Base(file))
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
