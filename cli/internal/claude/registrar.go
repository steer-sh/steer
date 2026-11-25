package claude

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// Registrar adapts Claude Code to the guard lifecycle: settings.json for the
// master hook, CLAUDE.md for the rules.
type Registrar struct{}

func (Registrar) Harness() harness.Harness {
	h, _ := harness.Get(harness.ClaudeCode)
	return h
}

func (Registrar) RegisterMaster(scope string) error { return RegisterMasterHook(scope) }

func (Registrar) RemoveMaster(scope string) error { return RemoveMasterHook(scope) }

func (Registrar) SetRules(rules []string, scope string) error { return SetRules(rules, scope) }

func (Registrar) InstructionPath(scope string) (string, error) { return GetClaudeMDFile(scope) }

// MasterRegistered reads settings.json rather than the store, because a hook file
// present in the store proves nothing about whether Claude Code runs it.
func (r Registrar) MasterRegistered(scope string) (bool, error) {
	path, err := r.RegisteredMasterPath(scope)
	return path != "", err
}

// RegisteredMasterPath reads back the master path settings.json names. It asks
// each command for a master path and keeps looking while the answer is empty,
// so an unrelated entry ahead of the master never reads as "no master".
func (Registrar) RegisteredMasterPath(scope string) (string, error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return "", err
	}
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if path := registeredMasterPath(hook.Command); path != "" {
				return path, nil
			}
		}
	}
	return "", nil
}

// registeredMasterPath reads the master path out of a settings.json command,
// without the anchor, so a project path stays relative to the config carrying it.
func registeredMasterPath(command string) string {
	return strings.TrimPrefix(hookstore.MasterPathFromCommand(command), projectDirPrefix)
}

// MasterCommandStale reports a registration naming the right master in an old form.
func (Registrar) MasterCommandStale(scope string) (bool, error) {
	return MasterCommandStale(scope)
}

// RegisteredMasterCommand reads back the command settings.json runs the master
// with. A reader comparing registrations across a rebuild needs the text: the
// path decodes the same whether or not it is quoted.
func (Registrar) RegisteredMasterCommand(scope string) (string, error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return "", err
	}
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if hookstore.IsMasterCommand(hook.Command) {
				return hook.Command, nil
			}
		}
	}
	return "", nil
}

var _ harness.Registrar = Registrar{}
