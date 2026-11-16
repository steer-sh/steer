// Package presets removes the guards an earlier Steer installed from its preset
// catalog. This Steer ships no preset, so every guard whose manifest source is
// "factory" is an orphan: it keeps running until the user removes it.
package presets

import (
	"fmt"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// Orphan is a guard an earlier Steer installed from a preset this Steer no longer ships.
type Orphan struct {
	HookName string
	SourceID string
}

// orphanReason is the archive reason every orphan removal records.
const orphanReason = "its preset is no longer shipped"

// Orphans lists the scope's orphan guards, sorted by id.
func Orphans(scope string) ([]Orphan, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	return orphansIn(manifest), nil
}

func orphansIn(manifest *claude.Manifest) []Orphan {
	var out []Orphan
	for name, entry := range manifest.Hooks {
		if strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) {
			out = append(out, Orphan{HookName: name, SourceID: strings.TrimSpace(entry.SourceID)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HookName < out[j].HookName })
	return out
}

// DisableOrphan removes the orphan guard named by its hook id or its preset name,
// and archives it, since no catalog holds a copy. found is false when the scope
// holds no such guard; a name that matches two guards is refused.
func DisableOrphan(scope, name string, targets ...harness.Registrar) (found bool, err error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return false, err
	}
	name = strings.TrimSpace(name)
	var matches []string
	for _, orphan := range orphansIn(manifest) {
		if orphan.HookName == name || orphan.SourceID == name {
			matches = append(matches, orphan.HookName)
		}
	}
	if len(matches) == 0 {
		return false, nil
	}
	if len(matches) > 1 {
		return true, fmt.Errorf("%s names more than one guard (%s); remove one with steer remove <id>", name, strings.Join(matches, ", "))
	}
	_, err = claude.RemoveManagedHook(scope, matches[0], claude.RemoveOptions{Reason: orphanReason}, targets...)
	return true, err
}

// PruneOrphans archives and removes every orphan guard in the scope, and returns
// them; under dryRun it returns them and changes nothing.
func PruneOrphans(scope string, dryRun bool, targets ...harness.Registrar) ([]Orphan, error) {
	orphans, err := Orphans(scope)
	if err != nil || dryRun {
		return orphans, err
	}
	for i, orphan := range orphans {
		if _, err := claude.RemoveManagedHook(scope, orphan.HookName, claude.RemoveOptions{Reason: orphanReason}, targets...); err != nil {
			return orphans[:i], err
		}
	}
	return orphans, nil
}
