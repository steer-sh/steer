package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
}

// InstallManagedHookResult reports install outcome details.
type InstallManagedHookResult struct {
	HookName     string
	FileName     string
	HookPath     string
	Overwritten  bool
	PreviousRule string
}

// RemoveManagedHookResult reports remove outcome details.
type RemoveManagedHookResult struct {
	HookName string
	HookPath string
	Rule     string
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
func InstallManagedHook(scope string, input InstallManagedHookInput) (*InstallManagedHookResult, error) {
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
	if fileName == MasterHookName {
		return nil, fmt.Errorf("cannot register %s directly", MasterHookName)
	}

	rule := strings.TrimSpace(input.Rule)
	if rule == "" {
		return nil, fmt.Errorf("rule cannot be empty")
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

	existingEntry, manifestExists := manifest.Hooks[hookName]

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
	if err := os.WriteFile(targetPath, updatedContent, 0o755); err != nil {
		return nil, fmt.Errorf("failed to write hook file: %w", err)
	}

	if err := SetHookEntry(scope, hookName, entry); err != nil {
		return nil, err
	}

	if err := ConsolidateHooks(scope); err != nil {
		return nil, err
	}

	if err := RegisterMasterHook(scope); err != nil {
		return nil, err
	}

	if manifestExists && strings.TrimSpace(existingEntry.Rule) != "" && NormalizeRule(existingEntry.Rule) != NormalizeRule(rule) {
		if err := RemoveRule(existingEntry.Rule, scope); err != nil {
			return nil, err
		}
	}
	if err := AppendRule(rule, scope); err != nil {
		return nil, err
	}

	return &InstallManagedHookResult{
		HookName:     hookName,
		FileName:     fileName,
		HookPath:     targetPath,
		Overwritten:  manifestExists,
		PreviousRule: existingEntry.Rule,
	}, nil
}

// RemoveManagedHook deletes one hook and re-syncs all managed artifacts.
func RemoveManagedHook(scope, identifier string) (*RemoveManagedHookResult, error) {
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

	var hookName, hookPath string
	var manifestEntry ManifestEntry
	var hasEntry bool

	if resolved, err := ResolveHook(scope, trimmedID); err == nil {
		hookName = resolved.Name
		hookPath = resolved.Path
		manifestEntry, hasEntry = manifest.Hooks[hookName]
	} else {
		hookName = normalizeHookName(trimmedID)
		if hookName == "" {
			return nil, err
		}
		manifestEntry, hasEntry = manifest.Hooks[hookName]

		hooksDir, dirErr := GetHooksDir(scope)
		if dirErr != nil {
			return nil, dirErr
		}
		if hasEntry && strings.TrimSpace(manifestEntry.File) != "" {
			hookPath = filepath.Join(hooksDir, manifestEntry.File)
		} else {
			hookPath = filepath.Join(hooksDir, hookName+".py")
		}
		if _, statErr := os.Stat(hookPath); os.IsNotExist(statErr) && !hasEntry {
			return nil, fmt.Errorf("hook not found: %s", identifier)
		}
	}

	if err := os.Remove(hookPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to delete hook file: %w", err)
	}

	rule := ""
	if hasEntry {
		rule = manifestEntry.Rule
		if err := RemoveHook(scope, hookName); err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(rule) != "" {
		if err := RemoveRule(rule, scope); err != nil {
			return nil, err
		}
	}

	if err := ConsolidateHooks(scope); err != nil {
		return nil, err
	}

	remaining, err := ListIndividualHooks(scope)
	if err != nil {
		return nil, err
	}
	if len(remaining) == 0 {
		if err := RemoveMasterHook(scope); err != nil {
			return nil, err
		}
	} else {
		if err := RegisterMasterHook(scope); err != nil {
			return nil, err
		}
	}

	return &RemoveManagedHookResult{
		HookName: hookName,
		HookPath: hookPath,
		Rule:     rule,
	}, nil
}
