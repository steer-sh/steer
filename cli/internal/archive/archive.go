// Package archive keeps a copy of a fix that a removal takes out of its store,
// so the fix can be put back together with the reason it was removed.
//
// An entry lives at <store>/archive/<id>/<entry>/ and holds the fix's files and
// an archive.json that describes them. Readers of a store skip its
// subdirectories, so an archived guard never reaches the consolidated master.
package archive

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version is the format version of archive.json.
const Version = 1

// DirName is the archive directory inside a store.
const DirName = "archive"

// EntryName is the file that describes one archive entry.
const EntryName = "archive.json"

// TimeLayout is RFC 3339 with a fixed nine-digit fraction.
const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// tempPrefix names an entry that is still being written. Readers skip it, so a
// write interrupted before its rename leaves nothing that reads as an entry.
const tempPrefix = ".tmp-"

// Entry describes one archived fix.
type Entry struct {
	Version int    `json:"version"`
	Tier    string `json:"tier"`
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	// Removed is the removal time in UTC, in TimeLayout.
	Removed string `json:"removed"`
	// Reason is why the fix was removed; empty when nobody gave one.
	Reason string `json:"reason,omitempty"`
	// Manifest is the fix's manifest entry as the store held it.
	Manifest json.RawMessage `json:"manifest,omitempty"`
	// Files names the files stored beside archive.json.
	Files []string `json:"files,omitempty"`
	// Missing names the parts of the fix the removal expected and did not find.
	Missing []string `json:"missing,omitempty"`
}

// Stored is an entry read back from a store, with the directory that holds it.
type Stored struct {
	Entry
	Dir string
}

// Write stores one entry and its files under storeDir and returns the entry's
// directory. The entry is complete when it appears: it is written to a
// temporary directory and renamed into place.
func Write(storeDir string, e Entry, files map[string][]byte) (string, error) {
	if err := checkSegment(e.ID); err != nil {
		return "", err
	}
	for name := range files {
		if err := checkSegment(name); err != nil {
			return "", err
		}
		if name == EntryName {
			return "", fmt.Errorf("archive file %q collides with %s", name, EntryName)
		}
	}

	now := time.Now().UTC()
	e.Version = Version
	e.Removed = now.Format(TimeLayout)
	e.Files = make([]string, 0, len(files))
	for name := range files {
		e.Files = append(e.Files, name)
	}
	sort.Strings(e.Files)
	sort.Strings(e.Missing)

	idDir := filepath.Join(storeDir, DirName, e.ID)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create archive directory: %w", err)
	}
	tmp, err := os.MkdirTemp(idDir, tempPrefix)
	if err != nil {
		return "", fmt.Errorf("failed to create archive entry: %w", err)
	}
	defer os.RemoveAll(tmp)

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), content, 0o644); err != nil {
			return "", fmt.Errorf("failed to write archived %s: %w", name, err)
		}
	}
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, EntryName), append(raw, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", EntryName, err)
	}

	suffix, err := randomSuffix()
	if err != nil {
		return "", err
	}
	final := filepath.Join(idDir, now.Format("20060102T150405.000000000Z")+"-"+suffix)
	if err := os.Rename(tmp, final); err != nil {
		return "", fmt.Errorf("failed to place archive entry: %w", err)
	}
	return final, nil
}

// List reads every entry under storeDir, oldest first. A store with no archive
// yields no entries.
func List(storeDir string) ([]Stored, error) {
	root := filepath.Join(storeDir, DirName)
	ids, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Stored, 0)
	for _, id := range ids {
		if !id.IsDir() {
			continue
		}
		stored, err := ForID(storeDir, id.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, stored...)
	}
	sortByRemoval(out)
	return out, nil
}

// ForID reads the entries archived under one id, oldest first. An id with no
// entries yields none.
func ForID(storeDir, id string) ([]Stored, error) {
	if err := checkSegment(id); err != nil {
		return nil, err
	}
	idDir := filepath.Join(storeDir, DirName, id)
	entries, err := os.ReadDir(idDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Stored, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() || partial(ent.Name()) {
			continue
		}
		s, err := Load(filepath.Join(idDir, ent.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sortByRemoval(out)
	return out, nil
}

func sortByRemoval(out []Stored) {
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].RemovedAt(), out[j].RemovedAt()
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return out[i].Dir < out[j].Dir
	})
}

// RemovedAt parses the entry's removal time. A time that does not parse reads as
// the zero time, so the entry sorts first.
func (s Stored) RemovedAt() time.Time {
	t, err := time.Parse(time.RFC3339Nano, s.Removed)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Name is the entry's directory name, which tells two entries of one id apart.
func (s Stored) Name() string { return filepath.Base(s.Dir) }

// Select picks the one entry a restore of id acts on. A non-empty kind keeps
// the entries of that tier and a non-empty name keeps the entry of that name.
// A choice that still matches more than one entry is refused, and the error
// names the flag that settles it.
func Select(entries []Stored, id, kind, name string) (Stored, error) {
	var matched []Stored
	tiers := map[string]bool{}
	for _, s := range entries {
		if s.ID != id {
			continue
		}
		tiers[s.Tier] = true
		if kind != "" && s.Tier != kind {
			continue
		}
		if name != "" && s.Name() != name {
			continue
		}
		matched = append(matched, s)
	}
	switch {
	case len(tiers) == 0:
		return Stored{}, fmt.Errorf("no archive entry for %q", id)
	case len(matched) == 0 && kind != "" && !tiers[kind]:
		return Stored{}, fmt.Errorf("no archived %s entry for %q; its entries are of tier %s",
			kind, id, strings.Join(sortedKeys(tiers), ", "))
	case len(matched) == 0:
		return Stored{}, fmt.Errorf("no archive entry named %q for %q", name, id)
	case len(matched) == 1:
		return matched[0], nil
	}
	if kind == "" && len(tiers) > 1 {
		return Stored{}, fmt.Errorf("%q holds archived entries of tiers %s; pass --tier",
			id, strings.Join(sortedKeys(tiers), ", "))
	}
	names := make([]string, len(matched))
	for i, s := range matched {
		names[i] = s.Name()
	}
	return Stored{}, fmt.Errorf("%s %q has %d archived entries; pass --entry with one of: %s",
		matched[0].Tier, id, len(matched), strings.Join(names, ", "))
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Load reads the entry stored in dir.
func Load(dir string) (Stored, error) {
	raw, err := os.ReadFile(filepath.Join(dir, EntryName))
	if err != nil {
		return Stored{}, err
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Stored{}, fmt.Errorf("failed to parse %s: %w", filepath.Join(dir, EntryName), err)
	}
	if e.Version != Version {
		return Stored{}, fmt.Errorf("%s: unsupported archive version %d: expected %d", dir, e.Version, Version)
	}
	return Stored{Entry: e, Dir: dir}, nil
}

// partial reports an entry directory that a write has not yet renamed into place.
func partial(name string) bool {
	return strings.HasPrefix(name, tempPrefix)
}

// checkSegment refuses a name that is not one plain path segment.
func checkSegment(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("archive name %q must be one path segment", name)
	}
	return nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
