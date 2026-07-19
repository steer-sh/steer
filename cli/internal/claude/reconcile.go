package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Drift reasons reported by DesiredHookDrift.
const (
	DriftMissingEntry = "missing_entry"
	DriftMetadata     = "metadata"
	DriftMissingFile  = "missing_file"
	DriftContent      = "content"
)

// Per-hook reconcile outcome statuses.
const (
	OutcomeUpToDate  = "up_to_date"
	OutcomeInstalled = "installed"
	OutcomeRemoved   = "removed"
	OutcomeKept      = "kept"
)

// DesiredHook is the target state for one managed hook, independent of the
// desired-state source (embedded preset catalog, team lockfile).
type DesiredHook struct {
	HookName string
	FileName string
	Rule     string
	Content  []byte
	Metadata HookInstallMetadata
}

// ReconcileOptions controls a Reconcile run.
type ReconcileOptions struct {
	DryRun bool
	// RemoveExtra decides whether a manifest entry absent from the desired
	// set is removed. Nil keeps all extras untouched and unreported.
	RemoveExtra func(hookName string, entry ManifestEntry) bool
}

// HookOutcome is the per-hook result of a Reconcile run.
type HookOutcome struct {
	HookName string
	Status   string
	Drift    string
}

// ReconcileResult aggregates Reconcile outcomes.
type ReconcileResult struct {
	Outcomes []HookOutcome
	Planned  int
}

// ContentSHA256 returns the hex sha256 of content.
func ContentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func desiredFileName(desired DesiredHook) string {
	fileName := strings.TrimSpace(desired.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(desired.HookName) + ".py"
	}
	return fileName
}

// DesiredFileContent returns the exact bytes a desired hook should have on
// disk after install (metadata header applied).
func DesiredFileContent(desired DesiredHook) ([]byte, error) {
	metadata, err := normalizeInstallMetadata(desired.Metadata)
	if err != nil {
		return nil, err
	}
	return applyMetadataHeader(desired.Content, metadata.Source, metadata.PromptVersion, metadata.HookVersion), nil
}

// DesiredHookDrift reports whether local state differs from the desired hook
// and why: manifest metadata mismatch, missing hook file, or content mismatch
// against the desired bytes.
func DesiredHookDrift(scope string, desired DesiredHook, entry ManifestEntry, hasEntry bool) (bool, string, error) {
	metadata, err := normalizeInstallMetadata(desired.Metadata)
	if err != nil {
		return false, "", err
	}
	fileName := desiredFileName(desired)

	if !hasEntry {
		return true, DriftMissingEntry, nil
	}
	if !strings.EqualFold(strings.TrimSpace(entry.Source), metadata.Source) {
		return true, DriftMetadata, nil
	}
	if strings.TrimSpace(entry.SourceID) != metadata.SourceID {
		return true, DriftMetadata, nil
	}
	if strings.TrimSpace(entry.SourceVersion) != metadata.SourceVersion {
		return true, DriftMetadata, nil
	}
	if strings.TrimSpace(entry.PromptVersion) != metadata.PromptVersion {
		return true, DriftMetadata, nil
	}
	if strings.TrimSpace(entry.HookVersion) != metadata.HookVersion {
		return true, DriftMetadata, nil
	}
	if file := strings.TrimSpace(entry.File); file != "" && file != fileName {
		return true, DriftMetadata, nil
	}
	if rule := strings.TrimSpace(desired.Rule); rule != "" && strings.TrimSpace(entry.RuleHash) != HashRule(rule) {
		return true, DriftMetadata, nil
	}

	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return false, "", err
	}
	targetPath := filepath.Join(hooksDir, fileName)
	actual, err := os.ReadFile(targetPath)
	if os.IsNotExist(err) {
		return true, DriftMissingFile, nil
	}
	if err != nil {
		return false, "", fmt.Errorf("failed reading hook %s: %w", targetPath, err)
	}

	expected, err := DesiredFileContent(desired)
	if err != nil {
		return false, "", err
	}
	if ContentSHA256(actual) != ContentSHA256(expected) {
		return true, DriftContent, nil
	}
	return false, "", nil
}

// Reconcile brings local managed-hook state in line with the desired set.
// Installs go through InstallManagedHook and removals through
// RemoveManagedHook, so master hook consolidation, settings registration, and
// CLAUDE.md rule sync stay consistent.
func Reconcile(scope string, desired []DesiredHook, opts ReconcileOptions) (*ReconcileResult, error) {
	result := &ReconcileResult{}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return result, err
	}

	desiredNames := make(map[string]struct{}, len(desired))
	for _, hook := range desired {
		desiredNames[hook.HookName] = struct{}{}
	}

	for _, hook := range desired {
		entry, hasEntry := manifest.Hooks[hook.HookName]
		drifted, reason, err := DesiredHookDrift(scope, hook, entry, hasEntry)
		if err != nil {
			return result, err
		}
		if !drifted {
			result.Outcomes = append(result.Outcomes, HookOutcome{HookName: hook.HookName, Status: OutcomeUpToDate})
			continue
		}
		result.Outcomes = append(result.Outcomes, HookOutcome{HookName: hook.HookName, Status: OutcomeInstalled, Drift: reason})
		if opts.DryRun {
			result.Planned++
			continue
		}
		if _, err := InstallManagedHook(scope, InstallManagedHookInput{
			HookName: hook.HookName,
			FileName: desiredFileName(hook),
			Rule:     hook.Rule,
			Content:  hook.Content,
			Metadata: hook.Metadata,
		}); err != nil {
			return result, err
		}
	}

	if opts.RemoveExtra == nil {
		return result, nil
	}

	extras := make([]string, 0)
	for hookName := range manifest.Hooks {
		if _, ok := desiredNames[hookName]; ok {
			continue
		}
		extras = append(extras, hookName)
	}
	sort.Strings(extras)

	for _, hookName := range extras {
		entry := manifest.Hooks[hookName]
		if !opts.RemoveExtra(hookName, entry) {
			result.Outcomes = append(result.Outcomes, HookOutcome{HookName: hookName, Status: OutcomeKept})
			continue
		}
		result.Outcomes = append(result.Outcomes, HookOutcome{HookName: hookName, Status: OutcomeRemoved})
		if opts.DryRun {
			result.Planned++
			continue
		}
		if _, err := RemoveManagedHook(scope, hookName); err != nil {
			return result, err
		}
	}

	return result, nil
}
