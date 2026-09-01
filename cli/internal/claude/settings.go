package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/hookstore"
)

const (
	ProjectScope = "project"
	GlobalScope  = "global"

	ClaudeDirName   = ".claude"
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

// StopHookEntry is a Stop-event group. Stop hooks carry no matcher, so this type
// round-trips hooks plus any unmodeled keys WITHOUT forcing a matcher onto entries
// steer does not own — HookEntry always emits one, which would rewrite the sibling Stop
// hooks in the file.
type StopHookEntry struct {
	Hooks []CommandHook

	extra map[string]json.RawMessage
}

func (e *StopHookEntry) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
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

func (e StopHookEntry) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range e.extra {
		raw[key] = value
	}
	hooks, err := json.Marshal(e.Hooks)
	if err != nil {
		return nil, err
	}
	raw["hooks"] = hooks
	return json.Marshal(raw)
}

// Hooks contains the different types of hook events. Only PreToolUse, PermissionDenied
// and Stop are modeled; every other event key (SessionStart, PostToolUse, ...) is
// carried through extra so a settings round-trip never drops hooks steer doesn't own.
type Hooks struct {
	PreToolUse       []HookEntry
	PermissionDenied []HookEntry
	Stop             []StopHookEntry
	extra            map[string]json.RawMessage
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
	if denied, ok := raw["PermissionDenied"]; ok {
		if err := json.Unmarshal(denied, &h.PermissionDenied); err != nil {
			return err
		}
		delete(raw, "PermissionDenied")
	}
	if stop, ok := raw["Stop"]; ok {
		if err := json.Unmarshal(stop, &h.Stop); err != nil {
			return err
		}
		delete(raw, "Stop")
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
	if len(h.PermissionDenied) > 0 {
		denied, err := json.Marshal(h.PermissionDenied)
		if err != nil {
			return nil, err
		}
		raw["PermissionDenied"] = denied
	}
	if len(h.Stop) > 0 {
		stop, err := json.Marshal(h.Stop)
		if err != nil {
			return nil, err
		}
		raw["Stop"] = stop
	} else {
		delete(raw, "Stop")
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

// GetHooksDir returns the scope-specific guard directory. The store is neutral
// and shared between harnesses, so it resolves through hookstore rather than
// sitting under the Claude Code config directory.
func GetHooksDir(scope string) (string, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}
	return hookstore.Resolve(scope)
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
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}
	return hookstore.MasterPath(scope)
}

// GetMasterHookCommand returns the settings.json command value for the master hook.
// Project scope intentionally uses a relative path.
func GetMasterHookCommand(scope string) (string, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", err
	}

	masterPath, err := GetMasterHookPath(scope)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("python3 %s", filepath.ToSlash(masterPath)), nil
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

// EnsureDirectories creates the guard directory for the scope and moves a legacy
// store into it. Every mutating entry point calls this before it reads a path.
func EnsureDirectories(scope string) error {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return err
	}
	_, err := hookstore.Prepare(scope)
	return err
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
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
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
	return hookstore.IsMasterCommand(command)
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
		return commitGateIdentityErr(command)
	}
	if err := EnsureDirectories(scope); err != nil {
		return err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	applyCommitGateHook(settings, command, timeout)
	return SaveSettings(scope, settings)
}

// RegisterCommitGateAndResolver arms the review hook and the resolver in ONE write, so
// the settings file never lands with the review armed and the resolver missing. The
// review writes the rows the resolver later reconciles; a half-armed install leaves the
// ledger permanently unresolved.
func RegisterCommitGateAndResolver(scope, review string, reviewTimeout int, backfill string, backfillTimeout int) error {
	if !IsCommitGateCommand(review) {
		return commitGateIdentityErr(review)
	}
	if !strings.Contains(backfill, backfillIdentity) {
		return backfillIdentityErr(backfill)
	}
	if err := EnsureDirectories(scope); err != nil {
		return err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	applyCommitGateHook(settings, review, reviewTimeout)
	applyBackfillHook(settings, backfill, backfillTimeout)
	return SaveSettings(scope, settings)
}

func commitGateIdentityErr(command string) error {
	return fmt.Errorf("refusing to register %q as the commit gate: the command must "+
		"contain %q or the hook cannot be recognised again", command, commitGateIdentity)
}

// applyCommitGateHook upserts the gate's PreToolUse entry in memory, removing any gate
// hook sitting under another matcher. It does not write.
func applyCommitGateHook(settings *Settings, command string, timeout int) {
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
}

// RemoveCommitGateHook drops the commit gate's PreToolUse entry, preserving every
// other hook.
func RemoveCommitGateHook(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	if !removeCommitGateHooks(settings) {
		return nil
	}
	return SaveSettings(scope, settings)
}

// removeCommitGateHooks drops the gate's PreToolUse entry in memory, preserving every
// other hook. It returns whether it removed anything and does not write.
func removeCommitGateHooks(settings *Settings) bool {
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
	settings.Hooks.PreToolUse = entries
	return removed
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
	// Command is the registered command verbatim. A reader reports the command the
	// harness invokes, never the one an installer would choose today.
	Command string
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

// backfillIdentity recognises steer's Go resolver Stop hook.
const backfillIdentity = "commit-gate backfill"

// legacyBackfillIdentity recognises the deprecated Python resolver Stop hook that
// install replaces.
const legacyBackfillIdentity = "backfill_ledger.py"

// IsBackfillCommand reports whether a Stop hook command is steer's resolver — the Go
// hook or the Python one it supersedes. Both are steer-owned and removed together.
func IsBackfillCommand(command string) bool {
	return strings.Contains(command, backfillIdentity) || strings.Contains(command, legacyBackfillIdentity)
}

func backfillIdentityErr(command string) error {
	return fmt.Errorf("refusing to register %q as the resolver: the command must "+
		"contain %q or the hook cannot be recognised again", command, backfillIdentity)
}

// RegisterBackfillHook upserts the resolver's Stop hook, replacing any recognised
// resolver command (the Go hook or the Python one it supersedes) and preserving every
// other Stop hook. A settings.json with no Stop groups gains one.
func RegisterBackfillHook(scope, command string, timeout int) error {
	if !strings.Contains(command, backfillIdentity) {
		return backfillIdentityErr(command)
	}
	if err := EnsureDirectories(scope); err != nil {
		return err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	applyBackfillHook(settings, command, timeout)
	return SaveSettings(scope, settings)
}

// applyBackfillHook upserts the resolver's Stop hook in memory. It does not write.
func applyBackfillHook(settings *Settings, command string, timeout int) {
	groups := stripBackfillHooks(settings.Hooks.Stop)
	hook := CommandHook{Type: "command", Command: command, Timeout: timeout}
	if len(groups) > 0 {
		groups[0].Hooks = append(groups[0].Hooks, hook)
	} else {
		groups = append(groups, StopHookEntry{Hooks: []CommandHook{hook}})
	}
	settings.Hooks.Stop = groups
}

// RemoveBackfillHook drops steer's resolver Stop hook, preserving every other Stop hook
// and removing a group left empty.
func RemoveBackfillHook(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	settings.Hooks.Stop = stripBackfillHooks(settings.Hooks.Stop)
	return SaveSettings(scope, settings)
}

// RemoveCommitGateAndResolver takes down both steer-owned Claude Code entries in ONE
// write, preserving unrelated PreToolUse and Stop hooks.
func RemoveCommitGateAndResolver(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	removeCommitGateHooks(settings)
	settings.Hooks.Stop = stripBackfillHooks(settings.Hooks.Stop)
	return SaveSettings(scope, settings)
}

// stripBackfillHooks returns the Stop groups with every resolver hook removed and any
// group left empty dropped.
func stripBackfillHooks(groups []StopHookEntry) []StopHookEntry {
	out := make([]StopHookEntry, 0, len(groups))
	for _, group := range groups {
		hooks := make([]CommandHook, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !IsBackfillCommand(hook.Command) {
				hooks = append(hooks, hook)
			}
		}
		if len(hooks) > 0 || len(group.extra) > 0 {
			group.Hooks = hooks
			out = append(out, group)
		}
	}
	return out
}

// BackfillArming is what a scope's settings.json says about the resolver.
type BackfillArming struct {
	Registered bool
	Timeout    int
	// Command is the registered command verbatim.
	Command string
}

// BackfillHookArming reads a scope's settings.json and reports the resolver's state.
func BackfillHookArming(scope string) BackfillArming {
	settings, err := LoadSettings(scope)
	if err != nil {
		return BackfillArming{}
	}
	for _, group := range settings.Hooks.Stop {
		for _, hook := range group.Hooks {
			if strings.Contains(hook.Command, backfillIdentity) {
				return BackfillArming{Registered: true, Timeout: hook.Timeout, Command: hook.Command}
			}
		}
	}
	return BackfillArming{}
}

// BackfillHookArmed reports whether steer's Go resolver Stop hook is registered.
func BackfillHookArmed(scope string) (bool, int) {
	a := BackfillHookArming(scope)
	return a.Registered, a.Timeout
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
				return CommitGateArming{Registered: true, Timeout: hook.Timeout,
					Matcher: entry.Matcher, Command: hook.Command}
			}
		}
	}
	return CommitGateArming{}
}
