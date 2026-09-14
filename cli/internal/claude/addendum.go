package claude

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/tier"
)

// ManagedAddendumEntry is one stored review addendum entry, read back with its id.
type ManagedAddendumEntry struct {
	ID string
	AddendumEntry
}

// InstallAddendumResult reports what an install replaced.
type InstallAddendumResult struct {
	ID           string
	Overwritten  bool
	PreviousText string
}

// InstallAddendum writes one review addendum entry into the scope's store. The
// commit gate reads the store on each review; no harness document is derived from
// it, so nothing is synced or consolidated.
func InstallAddendum(scope, id, text string) (*InstallAddendumResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	entryID, err := validateAddendumID(id)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("addendum text cannot be empty")
	}
	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	if _, taken := manifest.Hooks[entryID]; taken {
		return nil, fmt.Errorf("%q already names a guard in the %s store. One id names one artifact",
			entryID, scope)
	}
	if _, taken := manifest.Rules[entryID]; taken {
		return nil, fmt.Errorf("%q already names an instruction rule in the %s store. "+
			"One id names one artifact", entryID, scope)
	}
	hash := HashRule(text)
	for otherID, entry := range manifest.Addendum {
		if otherID != entryID && HashRule(entry.Text) == hash {
			return nil, fmt.Errorf("the %s store already holds this addendum entry as %q:\n  %s\n"+
				"Reword %q, or edit that entry with `steer commit-gate addendum add %s --text \"<text>\"`",
				scope, otherID, entry.Text, entryID, otherID)
		}
	}

	existing, overwritten := manifest.Addendum[entryID]
	now := time.Now().Format("2006-01-02")
	created := existing.Created
	if created == "" {
		created = now
	}
	entry := AddendumEntry{Text: text, TextHash: hash, Created: created, Updated: now}
	if err := SetAddendumEntry(scope, entryID, entry); err != nil {
		return nil, err
	}
	return &InstallAddendumResult{ID: entryID, Overwritten: overwritten, PreviousText: existing.Text}, nil
}

// RemoveAddendumResult reports the text a removal took out and where it was kept.
type RemoveAddendumResult struct {
	Text string
	// ArchivePath is the archive entry that holds the removed entry.
	ArchivePath string
}

// RemoveAddendum archives one review addendum entry with reason, then deletes
// it. A failed archive write stops the removal.
func RemoveAddendum(scope, id, reason string) (*RemoveAddendumResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	entryID, err := validateAddendumID(id)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	entry, ok := manifest.Addendum[entryID]
	if !ok {
		return nil, fmt.Errorf("addendum entry not found: %s", id)
	}
	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}
	archivePath, err := archiveTextEntry(scope, tier.Review, entryID, entry, reason)
	if err != nil {
		return nil, fmt.Errorf("failed to archive addendum entry %s, so it was not removed: %w", entryID, err)
	}
	if err := RemoveAddendumEntry(scope, entryID); err != nil {
		return nil, err
	}
	return &RemoveAddendumResult{Text: entry.Text, ArchivePath: archivePath}, nil
}

// ListAddendum reads one store's review addendum entries, ordered by id.
func ListAddendum(scope string) ([]ManagedAddendumEntry, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	out := make([]ManagedAddendumEntry, 0, len(manifest.Addendum))
	for id, entry := range manifest.Addendum {
		out = append(out, ManagedAddendumEntry{ID: id, AddendumEntry: entry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func validateAddendumID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("addendum id cannot be empty")
	}
	if strings.ContainsAny(id, " \t\n/\\") {
		return "", fmt.Errorf("addendum id %q carries whitespace or a path separator; use letters, digits, - and _", id)
	}
	return id, nil
}
