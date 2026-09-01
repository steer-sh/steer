package claude

import (
	"fmt"
	"strings"
)

// DenialsMatcher is the PermissionDenied matcher the denials recorder registers
// under. The event's matcher field is tool_name, and the recorder keeps every
// tool, so it matches all of them.
const DenialsMatcher = "*"

// denialsIdentity is how a settings.json hook command is recognised as the recorder.
const denialsIdentity = "denials record"

// IsDenialsCommand reports whether a settings.json hook command is the recorder.
func IsDenialsCommand(command string) bool {
	return strings.Contains(command, denialsIdentity)
}

// RegisterDenialsHook upserts the recorder's PermissionDenied entry under
// DenialsMatcher and drops any recorder hook sitting under another matcher.
//
// The command must pass IsDenialsCommand: a hook this package cannot recognise
// afterwards is one a later register duplicates instead of upserting.
func RegisterDenialsHook(scope, command string, timeout int) error {
	if !IsDenialsCommand(command) {
		return fmt.Errorf("refusing to register %q as the denials recorder: the command "+
			"must contain %q or the hook cannot be recognised again", command, denialsIdentity)
	}
	if err := EnsureDirectories(scope); err != nil {
		return err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	entries := stripDenialsHooks(settings.Hooks.PermissionDenied)
	entries = append(entries, HookEntry{
		Matcher: DenialsMatcher,
		Hooks: []CommandHook{{
			Type:    "command",
			Command: command,
			Timeout: timeout,
		}},
	})
	settings.Hooks.PermissionDenied = entries
	return SaveSettings(scope, settings)
}

// RemoveDenialsHook drops the recorder's PermissionDenied entry, preserving every
// other hook.
func RemoveDenialsHook(scope string) error {
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	if countDenialsHooks(settings.Hooks.PermissionDenied) == 0 {
		return nil
	}
	settings.Hooks.PermissionDenied = stripDenialsHooks(settings.Hooks.PermissionDenied)
	return SaveSettings(scope, settings)
}

// DenialsArming is what a scope's settings.json says about the recorder.
type DenialsArming struct {
	// Registered is true when a recorder command is present anywhere in
	// PermissionDenied.
	Registered bool
	// Matcher is the entry the hook sits under.
	Matcher string
	// Timeout is the wall clock the entry carries; zero means Claude Code applies
	// its own default.
	Timeout int
	// Command is the registered command verbatim.
	Command string
}

// DenialsHookArming reads a scope's settings.json and reports the recorder's state.
func DenialsHookArming(scope string) (DenialsArming, error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return DenialsArming{}, err
	}
	for _, entry := range settings.Hooks.PermissionDenied {
		for _, hook := range entry.Hooks {
			if IsDenialsCommand(hook.Command) {
				return DenialsArming{
					Registered: true,
					Matcher:    entry.Matcher,
					Timeout:    hook.Timeout,
					Command:    hook.Command,
				}, nil
			}
		}
	}
	return DenialsArming{}, nil
}

func stripDenialsHooks(entries []HookEntry) []HookEntry {
	kept := make([]HookEntry, 0, len(entries))
	for _, entry := range entries {
		hooks := make([]CommandHook, 0, len(entry.Hooks))
		for _, hook := range entry.Hooks {
			if !IsDenialsCommand(hook.Command) {
				hooks = append(hooks, hook)
			}
		}
		if len(hooks) > 0 {
			entry.Hooks = hooks
			kept = append(kept, entry)
		}
	}
	return kept
}

func countDenialsHooks(entries []HookEntry) int {
	n := 0
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if IsDenialsCommand(hook.Command) {
				n++
			}
		}
	}
	return n
}
