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
	hooks["Stop"] = upsertEntry(eventEntries(root, "Stop"), isCheckHook, entryValue(hookEntry{
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
	for _, raw := range eventEntries(root, "Stop") {
		entry, _ := raw.(map[string]any)
		hooks, _ := entry["hooks"].([]any)
		for _, rawHook := range hooks {
			hook, _ := rawHook.(map[string]any)
			command, _ := hook["command"].(string)
			if !match(command) {
				continue
			}
			a.Registered, a.Command = true, command
			if timeout, ok := hook["timeout"].(float64); ok {
				a.Timeout = int(timeout)
			}
			return a
		}
	}
	return a
}
