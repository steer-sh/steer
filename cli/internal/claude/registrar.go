package claude

import "github.com/steer-sh/steer-cli/internal/harness"

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
func (Registrar) MasterRegistered(scope string) (bool, error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return false, err
	}
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if IsSteerCommand(hook.Command) {
				return true, nil
			}
		}
	}
	return false, nil
}

var _ harness.Registrar = Registrar{}
