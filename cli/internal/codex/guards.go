package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// MasterMatcher is the matcher the guard master registers under. Codex applies a
// matcher as a regex over the canonical tool name, and a guard has to see both
// paths that change a repository: shell commands, reported as Bash, and file
// edits, reported as apply_patch.
const MasterMatcher = "Bash|apply_patch"

// MasterStatusMessage is the line Codex shows while the master runs.
const MasterStatusMessage = "checking the change against your rules ..."

// isMaster is deliberately not part of isSteerOwned. The guard master and the
// commit gate are independent channels on one event, and installing either one
// must leave the other registered.
func isMaster(command string) bool { return hookstore.IsMasterCommand(command) }

// MasterCommand is the hooks.json command value for the guard master. The master
// is one file serving both harnesses and reads its reply contract off this flag.
func MasterCommand(scope string) (string, error) {
	master, err := hookstore.MasterPath(scope)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("python3 %s %s %s", filepath.ToSlash(master), guard.HarnessFlag, harness.Codex), nil
}

// RegisterMaster upserts the guard master's PreToolUse handler, preserving every
// other key and every other steer channel.
func RegisterMaster(scope, cwd string) (string, error) {
	command, err := MasterCommand(scope)
	if err != nil {
		return "", err
	}
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return "", err
	}
	root, err := load(path)
	if err != nil {
		return "", err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	// Keep the timeout Codex already carries: steer owns the command, not the budget.
	timeout := 0
	for _, raw := range preToolUse(root) {
		entry, _ := raw.(map[string]any)
		list, _ := entry["hooks"].([]any)
		for _, rawHook := range list {
			hook, _ := rawHook.(map[string]any)
			if name, _ := hook["command"].(string); isMaster(name) {
				if value, ok := hook["timeout"].(float64); ok {
					timeout = int(value)
				}
			}
		}
	}
	hooks["PreToolUse"] = append(filterBy(preToolUse(root), isMaster), entryValue(hookEntry{
		Matcher: MasterMatcher,
		Hooks: []commandHook{{Type: "command", Command: command, Timeout: timeout,
			StatusMessage: MasterStatusMessage}},
	}))
	return path, save(path, root)
}

// RemoveMaster takes only the guard master out.
func RemoveMaster(scope, cwd string) (string, error) {
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return "", err
	}
	root, err := load(path)
	if err != nil {
		return "", err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return path, nil
	}
	setOrDrop(hooks, "PreToolUse", filterBy(preToolUse(root), isMaster))
	return path, save(path, root)
}

// MasterArming reports whether the guard master is registered, under which
// matcher, and whether config.toml holds a trust record at its current index.
func MasterArming(scope, cwd string) Arming {
	return armingFor(scope, cwd, isMaster)
}

// InstructionFile names the AGENTS.md this scope's rules are written to. Project
// scope stays relative, matching how the guard store names a project path.
func InstructionFile(scope, cwd string) (string, error) {
	entry, _ := harness.Get(harness.Codex)
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "project":
		return filepath.Join(cwd, entry.InstructionFile), nil
	case "global":
		home, err := codexHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, entry.InstructionFile), nil
	default:
		return "", fmt.Errorf("invalid scope %q", scope)
	}
}

// Registrar adapts Codex to the guard lifecycle: hooks.json for the master hook,
// AGENTS.md for the rules. Project paths resolve against the working directory,
// which is the repository root every steer command runs from.
type Registrar struct{}

func (Registrar) Harness() harness.Harness {
	h, _ := harness.Get(harness.Codex)
	return h
}

func (Registrar) RegisterMaster(scope string) error {
	_, err := RegisterMaster(scope, ".")
	return err
}

func (Registrar) RemoveMaster(scope string) error {
	_, err := RemoveMaster(scope, ".")
	return err
}

func (Registrar) AppendRule(rule, scope string) error {
	path, err := InstructionFile(scope, ".")
	if err != nil {
		return err
	}
	return instructions.AppendRule(path, rule)
}

func (Registrar) RemoveRule(rule, scope string) error {
	path, err := InstructionFile(scope, ".")
	if err != nil {
		return err
	}
	return instructions.RemoveRule(path, rule)
}

func (Registrar) InstructionPath(scope string) (string, error) {
	return InstructionFile(scope, ".")
}

func (Registrar) MasterRegistered(scope string) (bool, error) {
	return MasterArming(scope, ".").Registered, nil
}

// RegisteredMasterPath reads back the master path hooks.json names.
func (Registrar) RegisteredMasterPath(scope string) (string, error) {
	return hookstore.MasterPathFromCommand(MasterArming(scope, ".").Command), nil
}

var _ harness.Registrar = Registrar{}
