package claude

import (
	"fmt"
	"strings"
)

// checkHookIdentity is how a settings.json hook command is recognised as the
// repository-check Stop runner.
const checkHookIdentity = "check hook"

// CheckHookStatusMessage is the line Claude Code shows while check.sh runs.
const CheckHookStatusMessage = "running check.sh ..."

// IsCheckHookCommand reports whether a settings.json hook command is the Stop runner.
func IsCheckHookCommand(command string) bool {
	return strings.Contains(command, checkHookIdentity)
}

// RegisterCheckHook upserts the Stop runner as its own Stop group, replacing any
// runner command and preserving every other Stop hook, the resolver's included.
// Claude Code sends Stop for the main thread only, so no SubagentStop entry is
// written. The command must pass IsCheckHookCommand, or a later register would
// duplicate it instead of replacing it.
func RegisterCheckHook(scope, command string, timeout int) error {
	if !IsCheckHookCommand(command) {
		return fmt.Errorf("refusing to register %q as the check runner: the command must "+
			"contain %q or the hook cannot be recognised again", command, checkHookIdentity)
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	groups := stripStopHooks(settings.Hooks.Stop, IsCheckHookCommand)
	settings.Hooks.Stop = append(groups, StopHookEntry{Hooks: []CommandHook{{
		Type:          "command",
		Command:       command,
		Timeout:       timeout,
		StatusMessage: CheckHookStatusMessage,
	}}})
	return SaveSettings(scope, settings)
}

// RemoveCheckHook drops the Stop runner, preserving every other Stop hook. It
// reports whether a runner was registered; a scope with none is left unwritten.
func RemoveCheckHook(scope string) (bool, error) {
	return RemoveStopHooksMatching(scope, IsCheckHookCommand)
}

// RemoveStopHooksMatching drops every Stop hook whose command matches, keeping
// the rest of its group. It reports whether anything matched; a scope with no
// match is left unwritten.
func RemoveStopHooksMatching(scope string, match func(string) bool) (bool, error) {
	arming, err := StopHookMatching(scope, match)
	if err != nil || !arming.Registered {
		return false, err
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return false, err
	}
	settings.Hooks.Stop = stripStopHooks(settings.Hooks.Stop, match)
	return true, SaveSettings(scope, settings)
}

// CheckArming is what a scope's settings.json says about the Stop runner.
type CheckArming struct {
	Registered bool
	// Timeout is the wall clock the entry carries; zero means Claude Code applies
	// its own default.
	Timeout int
	// Command is the registered command verbatim.
	Command string
}

// CheckHookArming reads a scope's settings.json and reports the Stop runner's state.
func CheckHookArming(scope string) (CheckArming, error) {
	return StopHookMatching(scope, IsCheckHookCommand)
}

// StopHookMatching reports the first Stop hook in a scope whose command matches,
// so a caller can read a registration Steer does not own, such as the personal
// gate a migration replaces.
func StopHookMatching(scope string, match func(string) bool) (CheckArming, error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return CheckArming{}, err
	}
	for _, group := range settings.Hooks.Stop {
		for _, hook := range group.Hooks {
			if match(hook.Command) {
				return CheckArming{Registered: true, Timeout: hook.Timeout, Command: hook.Command}, nil
			}
		}
	}
	return CheckArming{}, nil
}
