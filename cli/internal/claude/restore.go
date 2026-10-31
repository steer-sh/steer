package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// RestoreResult reports an archived fix put back into its store.
type RestoreResult struct {
	Tier string
	ID   string
	// Verification is the fixture rerun that followed a guard's install; nil for
	// a rule or a review addendum entry.
	Verification *FixtureVerification
}

// RestoreArchived puts an archived guard, instruction rule or review addendum
// entry back into the scope's store through its tier's installer. The archive
// entry stays. An id its own tier holds now is refused, because each installer
// overwrites; the installers refuse an id another tier holds.
func RestoreArchived(scope string, s archive.Stored, targets ...harness.Registrar) (*RestoreResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	result := &RestoreResult{Tier: s.Tier, ID: s.ID}

	switch tier.Kind(s.Tier) {
	case tier.Guard:
		if _, held := manifest.Hooks[s.ID]; held {
			return nil, heldError("guard", s.ID, scope)
		}
		installed, err := restoreGuard(scope, s, targets...)
		if installed != nil {
			result.Verification = installed.Verification
		}
		if err != nil {
			return result, err
		}
	case tier.Instruction:
		if _, held := manifest.Rules[s.ID]; held {
			return nil, heldError("instruction rule", s.ID, scope)
		}
		var entry RuleEntry
		if err := decodeArchived(s, &entry); err != nil {
			return nil, err
		}
		if _, err := InstallManagedRule(scope, s.ID, entry.Text, targets...); err != nil {
			return nil, err
		}
	case tier.Review:
		if _, held := manifest.Addendum[s.ID]; held {
			return nil, heldError("review addendum entry", s.ID, scope)
		}
		var entry AddendumEntry
		if err := decodeArchived(s, &entry); err != nil {
			return nil, err
		}
		if _, err := InstallAddendum(scope, s.ID, entry.Text); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("archive entry %s holds a %s fix, which the hook store does not restore", s.Dir, s.Tier)
	}
	return result, nil
}

// restoreGuard installs an archived guard with its file, manifest metadata and
// fixtures. An entry that lacks the guard file or the manifest entry is refused,
// and so is a guard file in the store that no manifest entry names.
func restoreGuard(scope string, s archive.Stored, targets ...harness.Registrar) (*InstallManagedHookResult, error) {
	var entry ManifestEntry
	if err := decodeArchived(s, &entry); err != nil {
		return nil, err
	}
	// The archived metadata is decoded here, not read through LoadManifest, so
	// the name it carries is checked before it addresses either the archive or
	// the store.
	fileName, err := hookstore.EntryFileName(s.Dir, s.ID, entry.File)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(s.Dir, fileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("archive entry %s is missing the %s, so the guard cannot be restored whole", s.Dir, ArchiveMissingGuardFile)
	}
	if err != nil {
		return nil, err
	}

	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(hooksDir, fileName)
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("%s exists in the %s store with no manifest entry, and a restore would overwrite it. "+
			"Move it aside or register it, then restore", target, scope)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	var fixtures *fixture.File
	fixturesPath := filepath.Join(s.Dir, ArchivedFixturesName)
	raw, err := os.ReadFile(fixturesPath)
	switch {
	case err == nil:
		parsed, err := fixture.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("archived fixtures %s: %w", fixturesPath, err)
		}
		fixtures = &parsed
	case !os.IsNotExist(err):
		return nil, err
	}

	return InstallManagedHook(scope, InstallManagedHookInput{
		HookName: s.ID,
		FileName: fileName,
		Rule:     entry.Rule,
		Content:  content,
		Metadata: HookInstallMetadata{
			Source:        entry.Source,
			SourceID:      entry.SourceID,
			SourceVersion: entry.SourceVersion,
			PromptVersion: entry.PromptVersion,
			PromptModules: entry.PromptModules,
			HookVersion:   entry.HookVersion,
			Model:         entry.Model,
			SkillVersion:  entry.SkillVersion,
			SelfTest:      entry.SelfTest,
		},
		Fixtures: fixtures,
	}, targets...)
}

func decodeArchived(s archive.Stored, into any) error {
	if len(s.Manifest) == 0 {
		return fmt.Errorf("archive entry %s is missing the %s", s.Dir, ArchiveMissingManifest)
	}
	if err := json.Unmarshal(s.Manifest, into); err != nil {
		return fmt.Errorf("archive entry %s: %w", s.Dir, err)
	}
	return nil
}

func heldError(what, id, scope string) error {
	return fmt.Errorf("the %s store holds %s %q now, and a restore would overwrite it. "+
		"Remove it first to keep it in the archive too", scope, what, id)
}
