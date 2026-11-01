package codex

import (
	"fmt"
	"strings"
)

const (
	// checkHookIdentity marks the repository-check Stop runner so a later write
	// finds it again.
	checkHookIdentity      = "check hook"
	CheckHookStatusMessage = "running check.sh ..."
)

func isCheckHook(command string) bool { return strings.Contains(command, checkHookIdentity) }

// RegisterCheckHook upserts the Stop runner as its own Stop entry and returns the
// hooks file it wrote. It registers on Stop only: SubagentStop ends a subagent's
// thread, and check.sh runs for the root thread alone. Every other handler, the
// resolver's included, and every unrelated key is kept.
func RegisterCheckHook(scope, cwd, command string, timeout int) (string, error) {
	return ReplaceStopHook(scope, cwd, command, timeout, func(string) bool { return false })
}

// ReplaceStopHook registers the Stop runner in place of every Stop handler that
// replaces matches, in one write. The runner takes the slot of the first entry
// whose only handler is a runner or a replaced hook, so that entry adds no shift to
// the positions Codex keys approval by. Every other owned handler is removed, and
// an entry left empty moves the entries after it up one.
func ReplaceStopHook(scope, cwd, command string, timeout int, replaces func(string) bool) (string, error) {
	if !isCheckHook(command) {
		return "", fmt.Errorf("check runner command must contain %q", checkHookIdentity)
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
	owns := func(c string) bool { return isCheckHook(c) || replaces(c) }
	hooks["Stop"] = upsertEntry(eventEntries(root, "Stop"), owns, entryValue(hookEntry{
		Hooks: []commandHook{{Type: "command", Command: command, Timeout: timeout,
			StatusMessage: CheckHookStatusMessage}},
	}))
	return path, save(scope, path, root)
}

// RemoveCheckHook takes the Stop runner out and reports the hooks file and
// whether a runner was registered there. A file with none is left unwritten.
func RemoveCheckHook(scope, cwd string) (string, bool, error) {
	return RemoveStopHooksMatching(scope, cwd, isCheckHook)
}

// RemoveStopHooksMatching drops every Stop handler whose command matches, keeping
// the other handlers of its entry, and reports the hooks file and whether
// anything matched.
func RemoveStopHooksMatching(scope, cwd string, match func(string) bool) (string, bool, error) {
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return "", false, err
	}
	if !StopHookMatching(scope, cwd, match).Registered {
		return path, false, nil
	}
	root, err := load(path)
	if err != nil {
		return path, false, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	setOrDrop(hooks, "Stop", filterBy(eventEntries(root, "Stop"), match))
	return path, true, save(scope, path, root)
}

// CheckHookArming reports the Stop runner's registration, its timeout and the
// command it names. Trust is not read: Codex keys a trust record by the handler's
// event and position, and Steer reads that key only for PreToolUse.
func CheckHookArming(scope, cwd string) Arming {
	return StopHookMatching(scope, cwd, isCheckHook)
}

// StopHookMatching reports the first Stop handler whose command matches, so a
// caller can read a registration Steer does not own, such as the personal gate
// a migration replaces.
func StopHookMatching(scope, cwd string, match func(string) bool) Arming {
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return Arming{}
	}
	a := Arming{Path: path}
	root, err := load(path)
	if err != nil {
		return a
	}
	for i, raw := range eventEntries(root, "Stop") {
		entry, _ := raw.(map[string]any)
		hooks, _ := entry["hooks"].([]any)
		for j, rawHook := range hooks {
			hook, _ := rawHook.(map[string]any)
			command, _ := hook["command"].(string)
			if !match(command) {
				continue
			}
			a.Registered, a.Command = true, command
			a.Approval = approvalOf([]handlerSlot{slotAt(path, "Stop", i, j, entry, hook)})
			if timeout, ok := hook["timeout"].(float64); ok {
				a.Timeout = int(timeout)
			}
			return a
		}
	}
	return a
}
