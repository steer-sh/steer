package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// HookInstallMetadata carries provenance and generation metadata.
type HookInstallMetadata struct {
	Source        string
	SourceID      string
	SourceVersion string
	PromptVersion string
	PromptModules []string
	HookVersion   string
	Model         string
	SkillVersion  string
	SelfTest      *SelfTestSummary
}

// InstallManagedHookInput describes a managed install request.
type InstallManagedHookInput struct {
	HookName string
	FileName string
	Rule     string
	Content  []byte
	Metadata HookInstallMetadata
	// Fixtures are the guard's replayable test cases, stored beside it.
	Fixtures *fixture.File
}

// InstallManagedHookResult reports install outcome details.
type InstallManagedHookResult struct {
	HookName     string
	FileName     string
	HookPath     string
	Overwritten  bool
	PreviousRule string
	// Verification is the fixture rerun that followed consolidation. It is
	// reported, never a gate: a failing fixture does not undo the install.
	Verification *FixtureVerification
}

// RemoveOptions controls what a removal keeps.
type RemoveOptions struct {
	// Reason is why the guard is removed. The archive entry keeps it.
	Reason string
	// CatalogCopy marks a guard the preset catalog can install again, so the
	// removal writes no archive entry.
	CatalogCopy bool
}

// RemoveManagedHookResult reports remove outcome details.
type RemoveManagedHookResult struct {
	HookName string
	HookPath string
	Rule     string
	// ArchivePath is the archive entry that holds the removed guard; empty when
	// the removal wrote none.
	ArchivePath string
	// Verification is the fixture rerun that followed consolidation. A failing
	// fixture never blocks a removal.
	Verification *FixtureVerification
}

func normalizeInstallMetadata(metadata HookInstallMetadata) (HookInstallMetadata, error) {
	source, err := NormalizeSource(metadata.Source)
	if err != nil {
		return HookInstallMetadata{}, err
	}
	metadata.Source = source
	metadata.SourceID = strings.TrimSpace(metadata.SourceID)
	metadata.SourceVersion = strings.TrimSpace(metadata.SourceVersion)
	metadata.PromptVersion = strings.TrimSpace(metadata.PromptVersion)
	if metadata.PromptVersion == "" {
		metadata.PromptVersion = DefaultPromptVersion
	}
	metadata.HookVersion = strings.TrimSpace(metadata.HookVersion)
	if metadata.HookVersion == "" {
		if metadata.Source == SourceGenerated {
			metadata.HookVersion = metadata.PromptVersion
		} else {
			metadata.HookVersion = DefaultHookVersion
		}
	}
	metadata.Model = strings.TrimSpace(metadata.Model)
	metadata.SkillVersion = strings.TrimSpace(metadata.SkillVersion)
	metadata.PromptModules = normalizeStringList(metadata.PromptModules)
	return metadata, nil
}

func normalizeHookName(identifier string) string {
	name := strings.TrimSpace(identifier)
	if name == "" {
		return ""
	}
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, ".py")
	return strings.TrimSpace(name)
}

func applyMetadataHeader(content []byte, source, promptVersion, hookVersion string) []byte {
	source = strings.TrimSpace(source)
	if source == "" {
		source = SourceManual
	}
	promptVersion = strings.TrimSpace(promptVersion)
	if promptVersion == "" {
		promptVersion = DefaultPromptVersion
	}
	hookVersion = strings.TrimSpace(hookVersion)
	if hookVersion == "" {
		hookVersion = DefaultHookVersion
	}

	metaLine := fmt.Sprintf("# Steer-Meta: source=%s prompt_version=%s hook_version=%s", source, promptVersion, hookVersion)

	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []byte(metaLine + "\n")
	}

	insertAt := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
		insertAt = 1
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, metaLine)

	for i := insertAt; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "# Steer-Meta:") {
			continue
		}
		out = append(out, line)
	}

	updated := strings.Join(out, "\n")
	if strings.HasSuffix(text, "\n") && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	return []byte(updated)
}

// ApplyMetadataHeader returns hook content normalized with the steer metadata header.
func ApplyMetadataHeader(content []byte, source, promptVersion, hookVersion string) []byte {
	return applyMetadataHeader(content, source, promptVersion, hookVersion)
}

// InstallManagedHook writes an individual hook and syncs all managed artifacts.
// The guard store is shared, so the hook and the master are written once; each
// target then registers that one master and takes the rule into its own
// instruction document.
func InstallManagedHook(scope string, input InstallManagedHookInput, targets ...harness.Registrar) (*InstallManagedHookResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}

	hookName := normalizeHookName(input.HookName)
	if hookName == "" {
		return nil, fmt.Errorf("hook name cannot be empty")
	}

	fileName := strings.TrimSpace(input.FileName)
	if fileName == "" {
		fileName = hookName + ".py"
	}
	if strings.ToLower(filepath.Ext(fileName)) != ".py" {
		return nil, fmt.Errorf("hook file must be a .py file")
	}
	// The store owns its own directory, so the file name is a single local
	// basename. Joining an unchecked name reaches outside the store on write
	// and again when a removal deletes the path the manifest recorded.
	if !hookstore.LocalFileName(fileName) {
		return nil, fmt.Errorf("hook file %q must be a plain file name with no path separators", fileName)
	}
	if fileName == MasterHookName {
		return nil, fmt.Errorf("cannot register %s directly", MasterHookName)
	}
	if stem := strings.TrimSuffix(fileName, filepath.Ext(fileName)); !hookstore.GuardName(stem) || !hookstore.GuardName(hookName) {
		return nil, fmt.Errorf("hook file %q: a guard name holds only letters, digits, '_', '.' and '-', and starts with a letter or digit", fileName)
	}
	// The rebuild after the write refuses a store holding an unsafe name, so refuse
	// it here, before this install changes the store.
	existing, err := ListIndividualHooks(scope)
	if err != nil {
		return nil, fmt.Errorf("failed to list hooks: %w", err)
	}
	if err := refuseUnsafeGuardNames(scope, existing); err != nil {
		return nil, err
	}

	rule := strings.TrimSpace(input.Rule)
	if rule == "" {
		return nil, fmt.Errorf("rule cannot be empty")
	}
	if err := refuseMultilineRule(rule); err != nil {
		return nil, err
	}
	if len(input.Content) == 0 {
		return nil, fmt.Errorf("hook content cannot be empty")
	}

	metadata, err := normalizeInstallMetadata(input.Metadata)
	if err != nil {
		return nil, err
	}

	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}

	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}
	targetPath := filepath.Join(hooksDir, fileName)

	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	// One id names one artifact, refused on both write boundaries. Without the
	// mirror of InstallManagedRule's check, a guard installed over a rule's id
	// leaves the derived section carrying one of the two texts.
	if _, taken := manifest.Rules[hookName]; taken {
		return nil, fmt.Errorf("%q already names an instruction rule in the %s store. "+
			"Remove it with `steer rules remove %s`, or name the guard differently",
			hookName, scope, hookName)
	}
	if _, taken := manifest.Addendum[hookName]; taken {
		return nil, fmt.Errorf("%q already names a review addendum entry in the %s store. "+
			"Remove it with `steer commit-gate addendum remove %s`, or name the guard differently",
			hookName, scope, hookName)
	}

	// One file backs one id. Two ids sharing a file leave the second install's
	// content under both names, and a removal of either deletes the file the
	// other's manifest entry still points at.
	if otherName := fileOwner(manifest, hookName, fileName); otherName != "" {
		return nil, fmt.Errorf("hook file %q already belongs to %q in the %s store. "+
			"Remove it with `steer remove %s`, or name the file differently",
			fileName, otherName, scope, otherName)
	}

	existingEntry, manifestExists := manifest.Hooks[hookName]
	previousPath := ""
	if manifestExists {
		previousFile, err := hookstore.EntryFileName(filepath.Join(hooksDir, hookstore.ManifestName), hookName, existingEntry.File)
		if err != nil {
			return nil, err
		}
		if previousFile != fileName && fileOwner(manifest, hookName, previousFile) == "" {
			previousPath = filepath.Join(hooksDir, previousFile)
			if info, err := os.Lstat(previousPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("%s is a symlink; the guard store replaces only its own files", previousPath)
			} else if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	now := time.Now().Format("2006-01-02")
	created := existingEntry.Created
	if created == "" {
		created = now
	}

	entry := ManifestEntry{
		File:          fileName,
		Rule:          rule,
		Created:       created,
		Updated:       now,
		Source:        metadata.Source,
		SourceID:      metadata.SourceID,
		SourceVersion: metadata.SourceVersion,
		PromptVersion: metadata.PromptVersion,
		PromptModules: metadata.PromptModules,
		HookVersion:   metadata.HookVersion,
		Model:         metadata.Model,
		SkillVersion:  metadata.SkillVersion,
		RuleHash:      HashRule(rule),
		SelfTest:      metadata.SelfTest,
	}

	updatedContent := applyMetadataHeader(input.Content, entry.Source, entry.PromptVersion, entry.HookVersion)
	if err := hookstore.WriteFile(targetPath, updatedContent, 0o755); err != nil {
		return nil, fmt.Errorf("failed to write hook file: %w", err)
	}

	// Retire the old file while the manifest still names it, so a retry can
	// finish retirement before it forgets which implementation was replaced.
	if previousPath != "" {
		if err := os.Remove(previousPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to retire previous hook file: %w", err)
		}
	}

	if err := SetHookEntry(scope, hookName, entry); err != nil {
		return nil, err
	}

	if input.Fixtures != nil {
		stored := *input.Fixtures
		stored.Hook = hookName
		if err := fixture.Save(hooksDir, stored); err != nil {
			return nil, err
		}
	}

	if err := ConsolidateHooks(scope); err != nil {
		return nil, err
	}

	if err := SyncTargets(scope, targets...); err != nil {
		return nil, err
	}

	result := &InstallManagedHookResult{
		HookName:     hookName,
		FileName:     fileName,
		HookPath:     targetPath,
		Overwritten:  manifestExists,
		PreviousRule: existingEntry.Rule,
	}
	verification, err := VerifyFixtures(scope)
	if err != nil {
		return result, err
	}
	result.Verification = verification
	return result, nil
}

// fileOwner returns the lowest manifest id other than except whose entry owns
// file, or "". An entry with no File owns <key>.py. LoadManifest has already
// rejected a malformed entry, so one cannot hide an owner here.
func fileOwner(manifest *Manifest, except, file string) string {
	owner := ""
	for name, entry := range manifest.Hooks {
		if name == except {
			continue
		}
		owned, err := hookstore.EntryFileName(hookstore.ManifestName, name, entry.File)
		if err == nil && owned == file && (owner == "" || name < owner) {
			owner = name
		}
	}
	return owner
}

// RefuseHookRekey reports why RekeyManagedHook cannot give the guard from the id
// to and the file fileName.
func RefuseHookRekey(scope, from, to, fileName string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	if _, ok := manifest.Hooks[from]; !ok {
		return fmt.Errorf("hook not found in the %s store: %s", scope, from)
	}
	_, hookTaken := manifest.Hooks[to]
	_, ruleTaken := manifest.Rules[to]
	_, addendumTaken := manifest.Addendum[to]
	if hookTaken || ruleTaken || addendumTaken {
		return fmt.Errorf("cannot rename guard %q: %q is already in use in the %s store", from, to, scope)
	}
	if owner := fileOwner(manifest, from, fileName); owner != "" {
		return fmt.Errorf("cannot rename guard %q: hook file %q belongs to %q in the %s store", from, fileName, owner, scope)
	}
	return nil
}

// RekeyManagedHook moves a guard's manifest entry and fixtures to a new id in one
// manifest write. It deletes nothing the guard needs: the entry keeps naming the
// same file, so the guard stays installed whatever follows.
func RekeyManagedHook(scope, from, to, fileName string) error {
	if err := RefuseHookRekey(scope, from, to, fileName); err != nil {
		return err
	}
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return err
	}
	fixtures, hasFixtures, err := fixture.Load(hooksDir, from)
	if err != nil {
		return err
	}
	if hasFixtures {
		fixtures.Hook = to
		if err := fixture.Save(hooksDir, fixtures); err != nil {
			return err
		}
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	entry := manifest.Hooks[from]
	// An entry with no File owns <key>.py, so the new key must name it outright.
	entry.File, err = hookstore.EntryFileName(hookstore.ManifestName, from, entry.File)
	if err != nil {
		return err
	}
	// The master still carries the old id, so the moved entry reads as drift until
	// a reinstall writes a whole entry and rebuilds the master.
	entry.SourceVersion = ""
	manifest.Hooks[to] = entry
	delete(manifest.Hooks, from)
	if err := SaveManifest(scope, manifest); err != nil {
		return err
	}
	return RemoveFixtures(scope, from)
}

// RemoveManagedHook archives one hook, deletes it and re-syncs all managed
// artifacts. The hook is deleted only after its archive entry is in place, so a
// failed archive write leaves the hook installed. A file another entry also owns
// stays in the store for that entry.
func RemoveManagedHook(scope, identifier string, opts RemoveOptions, targets ...harness.Registrar) (*RemoveManagedHookResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}

	trimmedID := strings.TrimSpace(identifier)
	if trimmedID == "" {
		return nil, fmt.Errorf("hook identifier cannot be empty")
	}

	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	var hookName, hookFile, hookPath string
	var manifestEntry ManifestEntry
	var hasEntry bool

	// An exact manifest key names its own entry, even when another guard's file
	// has that stem.
	_, keyed := manifest.Hooks[trimmedID]
	resolved, err := HookFile{}, fmt.Errorf("hook not found: %s", identifier)
	if !keyed {
		resolved, err = ResolveHook(scope, trimmedID)
	}
	if err == nil {
		hookName = resolved.Name
		hookFile = resolved.FileName
		hookPath = resolved.Path
		manifestEntry, hasEntry = manifest.Hooks[hookName]
	} else {
		hookName = normalizeHookName(trimmedID)
		if keyed {
			hookName = trimmedID
		}
		if hookName == "" {
			return nil, err
		}
		manifestEntry, hasEntry = manifest.Hooks[hookName]

		hooksDir, dirErr := GetHooksDir(scope)
		if dirErr != nil {
			return nil, dirErr
		}
		manifestFile, manifestErr := GetManifestFile(scope)
		if manifestErr != nil {
			return nil, manifestErr
		}
		entryFile := ""
		if hasEntry {
			entryFile = manifestEntry.File
		}
		// The manifest names what this removal archives and deletes, so the name
		// is checked before it becomes a path. A repaired name would delete a
		// file the manifest never named.
		fileName, nameErr := hookstore.EntryFileName(manifestFile, hookName, entryFile)
		if nameErr != nil {
			return nil, nameErr
		}
		hookFile = fileName
		hookPath = filepath.Join(hooksDir, fileName)
		if _, statErr := os.Stat(hookPath); os.IsNotExist(statErr) && !hasEntry {
			// The store no longer names this hook, which is also what a removal
			// interrupted after the manifest write leaves behind. Sync first, so
			// the retry finishes the harness half it could not reach, then report
			// the hook as gone.
			if syncErr := SyncTargets(scope, targets...); syncErr != nil {
				return nil, syncErr
			}
			return nil, fmt.Errorf("hook not found: %s", identifier)
		}
	}

	archivePath := ""
	if !opts.CatalogCopy {
		archivePath, err = archiveHook(scope, hookName, hookFile, manifestEntry, hasEntry, opts.Reason)
		if err != nil {
			return nil, fmt.Errorf("failed to archive hook %s, so it was not removed: %w", hookName, err)
		}
	}

	if fileOwner(manifest, hookName, hookFile) == "" {
		if err := os.Remove(hookPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to delete hook file: %w", err)
		}
	}

	if err := RemoveFixtures(scope, hookName); err != nil {
		return nil, err
	}

	rule := ""
	if hasEntry {
		rule = manifestEntry.Rule
		if err := RemoveHook(scope, hookName); err != nil {
			return nil, err
		}
	}

	if err := ConsolidateHooks(scope); err != nil {
		return nil, err
	}

	if err := SyncTargets(scope, targets...); err != nil {
		return nil, err
	}

	result := &RemoveManagedHookResult{
		HookName:    hookName,
		HookPath:    hookPath,
		Rule:        rule,
		ArchivePath: archivePath,
	}
	verification, err := VerifyFixtures(scope)
	if err != nil {
		return result, err
	}
	result.Verification = verification
	return result, nil
}
