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
	AuditLogName    = "steer-audit.jsonl"
	ClaudeMDName    = "CLAUDE.md"
	BackupExtension = ".backup"
)

// CommandHook represents the innermost hook object. Only the four keys steer reads
// or writes are modeled; every other key rides extra so a settings round-trip never
// drops a field on a hook steer does not own.
type CommandHook struct {
	Type    string
	Command string
	// Timeout is the hook's wall-clock budget in seconds. Zero means the key is
	// absent and Claude Code applies its own default.
	Timeout int
	// StatusMessage is the line Claude Code shows while the hook runs. Empty
	// means the key is absent.
	StatusMessage string

	extra map[string]json.RawMessage
}

func (c *CommandHook) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, target := range map[string]any{
		"type":          &c.Type,
		"command":       &c.Command,
		"timeout":       &c.Timeout,
		"statusMessage": &c.StatusMessage,
	} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(value, target); err != nil {
			return err
		}
		delete(raw, key)
	}
	c.extra = raw
	return nil
}

func (c CommandHook) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range c.extra {
		raw[key] = value
	}
	for key, value := range map[string]any{
		"type":          c.Type,
		"command":       c.Command,
		"timeout":       c.Timeout,
		"statusMessage": c.StatusMessage,
	} {
		if isZero(value) {
			delete(raw, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw[key] = encoded
	}
	return json.Marshal(raw)
}

// isZero reports whether a modeled field is absent from the JSON object.
func isZero(value any) bool {
	switch v := value.(type) {
	case string:
		return v == ""
	case int:
		return v == 0
	}
	return false
}

// HookEntry represents a configuration for a set of hooks with a common matcher.
// Unmodeled keys ride extra, as they do on CommandHook.
type HookEntry struct {
	Matcher string
	Hooks   []CommandHook

	extra map[string]json.RawMessage
}

func (e *HookEntry) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if matcher, ok := raw["matcher"]; ok {
		if err := json.Unmarshal(matcher, &e.Matcher); err != nil {
			return err
		}
		delete(raw, "matcher")
	}
	if hooks, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooks, &e.Hooks); err != nil {
			return err
		}
		delete(raw, "hooks")
	}
	e.extra = raw
	return nil
}

func (e HookEntry) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range e.extra {
		raw[key] = value
	}
	matcher, err := json.Marshal(e.Matcher)
	if err != nil {
		return nil, err
	}
	raw["matcher"] = matcher
	hooks, err := json.Marshal(e.Hooks)
	if err != nil {
		return nil, err
	}
	raw["hooks"] = hooks
	return json.Marshal(raw)
}

// Hooks contains the different types of hook events. Only PreToolUse is
// modeled; every other event key (SessionStart, PostToolUse, ...) is carried
// through extra so a settings round-trip never drops hooks steer doesn't own.
type Hooks struct {
	PreToolUse []HookEntry
	extra      map[string]json.RawMessage
}

// Settings is the root object of the settings.json file. Only the hooks key
// is modeled; all other top-level keys (permissions, env, model, ...) are
// carried through extra unchanged.
type Settings struct {
	Hooks Hooks
	extra map[string]json.RawMessage
}

func (h *Hooks) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if pre, ok := raw["PreToolUse"]; ok {
		if err := json.Unmarshal(pre, &h.PreToolUse); err != nil {
			return err
		}
		delete(raw, "PreToolUse")
	}
	h.extra = raw
	return nil
}

func (h Hooks) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range h.extra {
		raw[key] = value
	}
	if len(h.PreToolUse) > 0 {
		pre, err := json.Marshal(h.PreToolUse)
		if err != nil {
			return nil, err
		}
		raw["PreToolUse"] = pre
	}
	return json.Marshal(raw)
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if hooks, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooks, &s.Hooks); err != nil {
			return err
		}
		delete(raw, "hooks")
	}
	s.extra = raw
	return nil
}

func (s Settings) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range s.extra {
		raw[key] = value
	}
	hooks, err := json.Marshal(s.Hooks)
	if err != nil {
		return nil, err
	}
	raw["hooks"] = hooks
	return json.Marshal(raw)
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
				// Keep the existing timeout: steer owns the command, not the budget.
				filteredHooks = append(filteredHooks, CommandHook{
					Type: "command", Command: masterCommand, Timeout: hook.Timeout,
				})
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

// CommitGateMatcher is the PreToolUse matcher the commit gate registers under. It
// matches Bash alone: the gate's own first act is to check the tool name, and a
// broader matcher spends a process on every tool call to reach the same answer.
const CommitGateMatcher = "Bash"

// commitGateIdentity is how a settings.json hook command is recognised as the gate.
const commitGateIdentity = "commit-gate review"

// CommitGateStatusMessage is the line Claude Code shows while the review runs.
const CommitGateStatusMessage = "red-teaming the commit ..."

// IsCommitGateCommand reports whether a settings.json hook command is the commit gate.
func IsCommitGateCommand(command string) bool {
	return strings.Contains(command, commitGateIdentity)
}

// RegisterCommitGateHook upserts the gate's PreToolUse entry under CommitGateMatcher,
// with an explicit timeout, and removes any gate hook sitting under another matcher.
//
// The command must pass IsCommitGateCommand: a hook this package cannot recognise
// afterwards is one a later register duplicates instead of upserting.
func RegisterCommitGateHook(scope, command string, timeout int) error {
	if !IsCommitGateCommand(command) {
		return fmt.Errorf("refusing to register %q as the commit gate: the command must "+
			"contain %q or the hook cannot be recognised again", command, commitGateIdentity)
	}
	if err := EnsureDirectories(scope); err != nil {
		return err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}

	entries := make([]HookEntry, 0, len(settings.Hooks.PreToolUse)+1)
	for _, entry := range settings.Hooks.PreToolUse {
		hooks := make([]CommandHook, 0, len(entry.Hooks))
		for _, hook := range entry.Hooks {
			if !IsCommitGateCommand(hook.Command) {
				hooks = append(hooks, hook)
			}
		}
		if len(hooks) > 0 {
			entry.Hooks = hooks
			entries = append(entries, entry)
		}
	}
	entries = append(entries, HookEntry{
		Matcher: CommitGateMatcher,
		Hooks: []CommandHook{{
			Type:          "command",
			Command:       command,
			Timeout:       timeout,
			StatusMessage: CommitGateStatusMessage,
		}},
	})

	settings.Hooks.PreToolUse = entries
	return SaveSettings(scope, settings)
}

// RemoveCommitGateHook drops the commit gate's PreToolUse entry, preserving every
// other hook.
func RemoveCommitGateHook(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	removed := false
	entries := make([]HookEntry, 0, len(settings.Hooks.PreToolUse))
	for _, entry := range settings.Hooks.PreToolUse {
		hooks := make([]CommandHook, 0, len(entry.Hooks))
		for _, hook := range entry.Hooks {
			if IsCommitGateCommand(hook.Command) {
				removed = true
				continue
			}
			hooks = append(hooks, hook)
		}
		if len(hooks) > 0 {
			entry.Hooks = hooks
			entries = append(entries, entry)
		}
	}
	if !removed {
		return nil
	}
	settings.Hooks.PreToolUse = entries
	return SaveSettings(scope, settings)
}

// CommitGateArming is what a scope's settings.json says about the gate.
type CommitGateArming struct {
	// Registered is true when a gate command is present anywhere in PreToolUse.
	Registered bool
	// Timeout is the wall clock the entry carries; zero means the key is absent and
	// Claude Code applies its own default.
	Timeout int
	// Matcher is the entry the hook sits under. Registered but not Effective means the
	// hook exists and will never run on a commit.
	Matcher string
}

// Effective reports whether the gate will actually fire on a Bash tool call.
//
// Registration alone is not the question an operator is asking. A hook whose entry
// carries a non-Bash matcher is present in the file and never invoked on a commit, so
// reporting it as armed describes the config while the commits land unreviewed — the
// silent failure this subsystem exists to prevent, one layer up.
func (a CommitGateArming) Effective() bool {
	return a.Registered && a.Matcher == CommitGateMatcher
}

// CommitGateHookArmed reports what this scope's settings.json says about the gate.
func CommitGateHookArmed(scope string) CommitGateArming {
	settings, err := LoadSettings(scope)
	if err != nil {
		return CommitGateArming{}
	}
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if IsCommitGateCommand(hook.Command) {
				return CommitGateArming{Registered: true, Timeout: hook.Timeout, Matcher: entry.Matcher}
			}
		}
	}
	return CommitGateArming{}
}
