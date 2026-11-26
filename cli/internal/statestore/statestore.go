// Package statestore resolves the evidence root on disk and moves a legacy
// corpus into the neutral root.
//
// A reader calls Resolve and gets whichever directory holds the component. A
// mutation calls Prepare, which copies a legacy component into the neutral root
// first. The copy never deletes.
package statestore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/steer-sh/steer-cli/internal/atomicfile"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// markerName is written last, inside a migrated component, and is the only
// signal that the neutral copy is authoritative. A component directory holds
// arbitrary shards with no inventory of its own, so an interrupted copy is
// unmarked and the next Prepare retries it whole.
const markerName = ".steer-state"

// Components names every corpus the evidence root holds. PrepareAll moves them
// together, because one command reads corpora another command wrote and a root
// half in each place is a corpus nobody can score.
var Components = []string{
	"commit-review",
	"firing-audits",
	"mods",
	"steer-report",
	"transcript-sessions",
}

// Resolve names the directory a reader should read. It is the legacy directory
// only while that one holds a corpus and the neutral one is not yet marked, so
// a machine that has never migrated keeps working and a migrated machine never
// reads back.
func Resolve(component string) string {
	neutral := steerpath.StateDir(component)
	if migrated(neutral) {
		return neutral
	}
	legacy := steerpath.LegacyStateDir(component)
	if legacy != neutral && populated(legacy) {
		return legacy
	}
	return neutral
}

// Prepare names the directory a mutation must write to, creating it and copying
// a legacy corpus into it first. The copy is additive: a file already in the
// neutral root is kept and its legacy counterpart is left where it is.
func Prepare(component string) (string, error) {
	neutral := steerpath.StateDir(component)
	if err := os.MkdirAll(neutral, 0o700); err != nil {
		return "", fmt.Errorf("failed to create state directory: %w", err)
	}
	// The root holds review prompts and transcript signals that quote code, so no
	// other local user may enter it, whatever modes the files below it carry.
	if err := ClosePrivate(filepath.Dir(neutral)); err != nil {
		return "", fmt.Errorf("failed to make the state directory private: %w", err)
	}
	// A migration leaves the legacy copy in place, so close that directory too.
	legacy := steerpath.LegacyStateDir(component)
	if legacy != neutral && populated(legacy) {
		if err := ClosePrivate(legacy); err != nil {
			return "", fmt.Errorf("failed to make %s private: %w", legacy, err)
		}
	}
	if migrated(neutral) {
		return neutral, nil
	}
	if legacy != neutral && populated(legacy) {
		if err := migrate(legacy, neutral); err != nil {
			return "", err
		}
	}
	if err := atomicfile.WriteFile(filepath.Join(neutral, markerName), nil, 0o644); err != nil {
		return "", fmt.Errorf("failed to mark %s migrated: %w", component, err)
	}
	return neutral, nil
}

// ClosePrivate removes the group and other permission bits from dir and keeps the
// owner's, so a directory its owner made read-only stays read-only.
func ClosePrivate(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return os.Chmod(dir, perm&^0o077)
	}
	return nil
}

// PrepareAll migrates every component. A writing entry point calls it once,
// before it resolves any path, so nothing in the same process holds a path from
// the root the migration just left.
func PrepareAll() error {
	for _, component := range Components {
		if _, err := Prepare(component); err != nil {
			return err
		}
	}
	return nil
}

// Migrated reports whether the neutral root owns a component.
func Migrated(component string) bool { return migrated(steerpath.StateDir(component)) }

func migrated(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, markerName))
	return err == nil
}

// populated reports whether a directory holds anything at all. A component
// directory is Steer's alone, so every entry in it belongs to the corpus.
func populated(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// migrate copies a legacy component into the neutral root, subdirectories and
// all. It never overwrites a file already in the neutral root, and it writes
// each file through a temporary name in its destination directory.
func migrate(legacy, neutral string) error {
	names, err := relativeFiles(legacy)
	if err != nil {
		return err
	}
	for _, name := range names {
		src := filepath.Join(legacy, name)
		dst := filepath.Join(neutral, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("failed to create %s: %w", filepath.Dir(dst), err)
		}
		if err := copyWhole(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s: %w", name, err)
		}
	}
	return nil
}

// relativeFiles lists the regular files under dir, relative to it and sorted, so
// a migration copies the same corpus in the same order on every machine.
func relativeFiles(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", dir, err)
	}
	sort.Strings(names)
	return names, nil
}

// copyWhole streams src to dst, replacing dst whole. The
// corpus holds saved diffs and a ledger of many megabytes, so the copy streams
// rather than staging the whole file in memory.
func copyWhole(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o644)
	if info, err := in.Stat(); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicfile.Copy(dst, in, mode)
}
