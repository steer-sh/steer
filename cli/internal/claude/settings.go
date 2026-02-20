package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProjectScope = "project"
	GlobalScope  = "global"

	ClaudeDirName   = ".claude"
	HooksDirName    = "hooks"
	SettingsName    = "settings.json"
	MasterHookName  = "steer-hooks.py"
	ManifestName    = "steer-manifest.json"
	ClaudeMDName    = "CLAUDE.md"
	BackupExtension = ".backup"
)

// CommandHook represents the innermost hook object.
type CommandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookEntry represents a configuration for a set of hooks with a common matcher.
type HookEntry struct {
	Matcher string        `json:"matcher"`
	Hooks   []CommandHook `json:"hooks"`
}

// Hooks contains the different types of hook events.
type Hooks struct {
	PreToolUse []HookEntry `json:"PreToolUse,omitempty"`
}

// Settings is the root object of the settings.json file.
type Settings struct {
	Hooks Hooks `json:"hooks"`
}

// ValidateScope ensures scope is one of the supported values.
func ValidateScope(scope string) error {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ProjectScope, GlobalScope:
		return nil
	default:
		return fmt.Errorf("invalid scope %q: expected %q or %q", scope, ProjectScope, GlobalScope)
	}
}

func normalizeScope(scope string) string {
	return strings.ToLower(strings.TrimSpace(scope))
}

// GetClaudeDir returns the scope-specific .claude directory.
func GetClaudeDir(scope string) (string, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}

	if scope == GlobalScope {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve home directory: %w", err)
		}
		return filepath.Join(home, ClaudeDirName), nil
	}

	return ClaudeDirName, nil
}

// GetHooksDir returns the scope-specific hooks directory.
func GetHooksDir(scope string) (string, error) {
	claudeDir, err := GetClaudeDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, HooksDirName), nil
}

// GetSettingsFile returns the scope-specific settings.json path.
func GetSettingsFile(scope string) (string, error) {
	claudeDir, err := GetClaudeDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, SettingsName), nil
}

// GetMasterHookPath returns the scope-specific master hook file path.
func GetMasterHookPath(scope string) (string, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(hooksDir, MasterHookName), nil
}

// GetMasterHookCommand returns the settings.json command value for the master hook.
// Project scope intentionally uses a relative path.
func GetMasterHookCommand(scope string) (string, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}

	if scope == GlobalScope {
		masterPath, err := GetMasterHookPath(scope)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("python3 %s", masterPath), nil
	}

	return fmt.Sprintf("python3 %s", filepath.ToSlash(filepath.Join(ClaudeDirName, HooksDirName, MasterHookName))), nil
}

// GetManifestFile returns the scope-specific manifest path.
func GetManifestFile(scope string) (string, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(hooksDir, ManifestName), nil
}

// GetClaudeMDFile returns the scope-specific CLAUDE.md path.
func GetClaudeMDFile(scope string) (string, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}

	if scope == GlobalScope {
		claudeDir, err := GetClaudeDir(scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(claudeDir, ClaudeMDName), nil
	}

	return ClaudeMDName, nil
}

// EnsureDirectories creates .claude and hooks directories for the scope.
func EnsureDirectories(scope string) error {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}
	return nil
}

// LoadSettings reads and parses the settings.json file.
func LoadSettings(scope string) (*Settings, error) {
	settingsPath, err := GetSettingsFile(scope)
	if err != nil {
		return nil, err
	}

	var settings Settings
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		return &settings, nil
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &settings, nil
	}

	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings.json: %w", err)
	}

	return &settings, nil
}

// SaveSettings writes the settings to settings.json with pretty formatting.
func SaveSettings(scope string, settings *Settings) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	settingsPath, err := GetSettingsFile(scope)
	if err != nil {
		return err
	}

	if err := createBackup(settingsPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write settings file: %w", err)
	}

	return nil
}

func createBackup(settingsPath string) error {
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		return nil
	}

	input, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}

	return os.WriteFile(settingsPath+BackupExtension, input, 0o644)
}

// IsSteerCommand checks whether a hook command references steer-hooks.py.
func IsSteerCommand(command string) bool {
	return strings.Contains(command, MasterHookName)
}

// RegisterMasterHook upserts the steer master hook while preserving non-steer hooks.
func RegisterMasterHook(scope string) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}

	masterCommand, err := GetMasterHookCommand(scope)
	if err != nil {
		return err
	}

	found := false
	updatedEntries := make([]HookEntry, 0, len(settings.Hooks.PreToolUse))

	for _, entry := range settings.Hooks.PreToolUse {
		filteredHooks := make([]CommandHook, 0, len(entry.Hooks))
		for _, hook := range entry.Hooks {
			if !IsSteerCommand(hook.Command) {
				filteredHooks = append(filteredHooks, hook)
				continue
			}

			if !found {
				filteredHooks = append(filteredHooks, CommandHook{Type: "command", Command: masterCommand})
				found = true
			}
		}

		if len(filteredHooks) > 0 {
			entry.Hooks = filteredHooks
			updatedEntries = append(updatedEntries, entry)
		}
	}

	if !found {
		updatedEntries = append(updatedEntries, HookEntry{
			Matcher: ".*",
			Hooks: []CommandHook{{
				Type:    "command",
				Command: masterCommand,
			}},
		})
	}

	settings.Hooks.PreToolUse = updatedEntries
	return SaveSettings(scope, settings)
}

// RemoveMasterHook removes steer master hook commands and preserves non-steer hooks.
func RemoveMasterHook(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}

	removed := false
	updatedEntries := make([]HookEntry, 0, len(settings.Hooks.PreToolUse))

	for _, entry := range settings.Hooks.PreToolUse {
		filteredHooks := make([]CommandHook, 0, len(entry.Hooks))
		for _, hook := range entry.Hooks {
			if IsSteerCommand(hook.Command) {
				removed = true
				continue
			}
			filteredHooks = append(filteredHooks, hook)
		}

		if len(filteredHooks) > 0 {
			entry.Hooks = filteredHooks
			updatedEntries = append(updatedEntries, entry)
		}
	}

	if !removed {
		return nil
	}

	settings.Hooks.PreToolUse = updatedEntries
	return SaveSettings(scope, settings)
}
