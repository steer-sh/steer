package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// Parts of a guard that an archive entry names as missing.
const (
	ArchiveMissingGuardFile = "guard file"
	ArchiveMissingManifest  = "manifest entry"
)

// ArchivedFixturesName is the file an archive entry stores a guard's fixtures in.
const ArchivedFixturesName = "fixtures.json"

// archiveHook stores a guard's file, fixtures and manifest entry in the scope's
// archive and returns the entry's directory. Each part is copied as raw bytes,
// so a guard whose fixtures no longer parse is still archived whole.
// archiveHook copies a guard, its fixtures and its manifest entry into the
// archive before a removal deletes them. hookFile is a name inside the store,
// never a path: both reads go through the store root, so a symlink that leaves
// the store cannot put another file's contents in the archive.
func archiveHook(scope, hookName, hookFile string, entry ManifestEntry, hasEntry bool, reason string) (string, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	store, err := os.OpenRoot(hooksDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if store != nil {
		defer store.Close()
	}

	e := archive.Entry{
		Tier:   string(tier.Guard),
		ID:     hookName,
		Scope:  scope,
		Reason: reason,
	}
	files := map[string][]byte{}

	content, err := readInStore(store, hookFile)
	switch {
	case err == nil:
		files[hookFile] = content
	case os.IsNotExist(err):
		e.Missing = append(e.Missing, ArchiveMissingGuardFile)
	default:
		return "", err
	}

	fixturesRel, err := filepath.Rel(hooksDir, fixture.Path(hooksDir, hookName))
	if err != nil {
		return "", err
	}
	fixtures, err := readInStore(store, fixturesRel)
	switch {
	case err == nil:
		files[ArchivedFixturesName] = fixtures
	case !os.IsNotExist(err):
		return "", err
	}

	if hasEntry {
		raw, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		e.Manifest = raw
	} else {
		e.Missing = append(e.Missing, ArchiveMissingManifest)
	}

	return archive.Write(hooksDir, e, files)
}

// archiveTextEntry stores a rule or review addendum entry in the scope's
// archive. The entry's text lives in its manifest entry, so no file is stored.
func archiveTextEntry(scope string, kind tier.Kind, id string, entry any, reason string) (string, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	return archive.Write(hooksDir, archive.Entry{
		Tier:     string(kind),
		ID:       id,
		Scope:    scope,
		Reason:   reason,
		Manifest: raw,
	}, nil)
}

// readInStore reads one file named relative to an open store root. A nil root is
// a store that does not exist, which reads as a missing file.
func readInStore(store *os.Root, name string) ([]byte, error) {
	if store == nil {
		return nil, os.ErrNotExist
	}
	f, err := store.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
