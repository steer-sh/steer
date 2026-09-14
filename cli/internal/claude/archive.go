package claude

import (
	"encoding/json"
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
func archiveHook(scope, hookName, hookPath string, entry ManifestEntry, hasEntry bool, reason string) (string, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return "", err
	}

	e := archive.Entry{
		Tier:   string(tier.Guard),
		ID:     hookName,
		Scope:  scope,
		Reason: reason,
	}
	files := map[string][]byte{}

	content, err := os.ReadFile(hookPath)
	switch {
	case err == nil:
		files[filepath.Base(hookPath)] = content
	case os.IsNotExist(err):
		e.Missing = append(e.Missing, ArchiveMissingGuardFile)
	default:
		return "", err
	}

	fixtures, err := os.ReadFile(fixture.Path(hooksDir, hookName))
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
