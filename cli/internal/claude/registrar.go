package claude

import (
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

func (Registrar) AppendRule(rule, scope string) error { return AppendRule(rule, scope) }

func (Registrar) RemoveRule(rule, scope string) error { return RemoveRule(rule, scope) }

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
			if path := hookstore.MasterPathFromCommand(hook.Command); path != "" {
				return path, nil
			}
		}
	}
	return "", nil
}

var _ harness.Registrar = Registrar{}
