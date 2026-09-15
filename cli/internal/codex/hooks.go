// Package codex manages Codex-native hook registration.
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// CommitGateMatcher is the canonical hook tool name Codex reports for a shell
	// command. LegacyCommitGateMatcher is what older builds sent; a registration
	// carrying it matches nothing and is reported as inert.
	CommitGateMatcher       = "Bash"
	LegacyCommitGateMatcher = "exec_command"
	commitGateIdentity      = "commit-gate review-codex"
	CommitGateStatusMessage = "red-teaming the commit ..."
	// resolverIdentity marks the resolver's handlers so removal finds them again.
	resolverIdentity = "commit-gate backfill"
)

// resolverEvents are the two turn-end events the resolver runs on. A gate that fires
// inside a subagent is adjudicated there, and only SubagentStop follows that thread.
var resolverEvents = []string{"Stop", "SubagentStop"}

type commandHook struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	// AdditionalContextLimit is the token threshold past which Codex writes a hook's
	// additionalContext to a temporary file and hands the model a preview instead. The
	// gate's review is the model's whole task, so the gate registers 0, which disables
	// the spill. It is a pointer because 0 is the value being written.
	AdditionalContextLimit *int `json:"additionalContextLimit,omitempty"`
}

// noContextSpill is the additionalContextLimit the commit gate registers.
var noContextSpill = new(int)

type hookEntry struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []commandHook `json:"hooks"`
}

// Arming describes the gate entry and whether config.toml contains a trust record at
// its current event/entry/handler index. Codex's handler hash algorithm is internal;
// record presence alone does not prove that the current definition is trusted.
type Arming struct {
	Registered         bool
	Matcher            string
	Timeout            int
	TrustRecordPresent bool
	TrustStateKnown    bool
	Path               string
	// Command is the registered hook command verbatim, so a reader can name the
	// file this harness actually invokes rather than the one a store resolves to.
	Command string
}

func (a Arming) Effective() bool { return a.Registered && a.Matcher == CommitGateMatcher }

func trustSection(key string) string { return "[hooks.state." + strconv.Quote(key) + "]" }

func hooksPath(scope, cwd string) (string, error) {
	if scope == "global" {
		home, err := codexHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "hooks.json"), nil
	}
	if scope != "project" {
		return "", fmt.Errorf("invalid scope %q", scope)
	}
	return filepath.Join(cwd, ".codex", "hooks.json"), nil
}

func codexHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func load(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

func eventEntries(root map[string]any, event string) []any {
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	return entries
}

func preToolUse(root map[string]any) []any { return eventEntries(root, "PreToolUse") }

func isGate(command string) bool { return strings.Contains(command, commitGateIdentity) }

func isResolver(command string) bool { return strings.Contains(command, resolverIdentity) }

func isSteerOwned(command string) bool { return isGate(command) || isResolver(command) }

// filter drops this gate's own handlers, keeping every other key at every level.
func filter(entries []any) []any { return filterBy(entries, isGate) }

func filterBy(entries []any, drop func(string) bool) []any {
	out := make([]any, 0, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		hooks, _ := entry["hooks"].([]any)
		if hooks == nil {
			out = append(out, raw)
			continue
		}
		kept := make([]any, 0, len(hooks))
		for _, rawHook := range hooks {
			hook, ok := rawHook.(map[string]any)
			command, _ := hook["command"].(string)
			if !ok || !drop(command) {
				kept = append(kept, rawHook)
			}
		}
		if len(kept) > 0 {
			entry["hooks"] = kept
			out = append(out, entry)
		}
	}
	return out
}

// upsertEntry puts fresh at the index of the first entry whose only handler owns
// names, and drops every other handler owns names. Codex keys a hook's trust record
// by event, entry index and handler index, so re-registering a handler at a new
// index voids its approval and the approval of whatever takes its old place. With
// no such entry, fresh is appended.
func upsertEntry(entries []any, owns func(string) bool, fresh any) []any {
	slot := -1
	for i, raw := range entries {
		if soleHandlerOwned(raw, owns) {
			slot = i
			break
		}
	}
	if slot < 0 {
		return append(filterBy(entries, owns), fresh)
	}
	out := make([]any, 0, len(entries))
	for i, raw := range entries {
		if i == slot {
			out = append(out, fresh)
			continue
		}
		out = append(out, filterBy([]any{raw}, owns)...)
	}
	return out
}

func soleHandlerOwned(raw any, owns func(string) bool) bool {
	entry, _ := raw.(map[string]any)
	hooks, _ := entry["hooks"].([]any)
	if len(hooks) != 1 {
		return false
	}
	hook, _ := hooks[0].(map[string]any)
	command, _ := hook["command"].(string)
	return owns(command)
}

func save(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hooks.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// RegisterCommitGate upserts one Bash PreToolUse handler while preserving unrelated
// JSON keys at every level.
func RegisterCommitGate(scope, cwd, command string, timeout int) (string, error) {
	return RegisterCommitGateAndResolver(scope, cwd, command, timeout, "", 0)
}

// RegisterCommitGateAndResolver writes the review hook and the resolver in one save.
// The review writes the rows the resolver later reconciles from the rollout, so a
// half-armed install leaves the ledger permanently unresolved. An empty resolver
// command arms the review channel alone.
func RegisterCommitGateAndResolver(scope, cwd, review string, reviewTimeout int,
	resolver string, resolverTimeout int) (string, error) {
	if !isGate(review) {
		return "", fmt.Errorf("commit gate command must contain %q", commitGateIdentity)
	}
	if resolver != "" && !isResolver(resolver) {
		return "", fmt.Errorf("resolver command must contain %q", resolverIdentity)
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
	hooks["PreToolUse"] = upsertEntry(preToolUse(root), isSteerOwned, entryValue(hookEntry{
		Matcher: CommitGateMatcher,
		Hooks: []commandHook{{Type: "command", Command: review, Timeout: reviewTimeout,
			StatusMessage:          CommitGateStatusMessage,
			AdditionalContextLimit: noContextSpill}},
	}))
	for _, event := range resolverEvents {
		if resolver == "" {
			setOrDrop(hooks, event, filterBy(eventEntries(root, event), isSteerOwned))
			continue
		}
		// No matcher: Stop ignores one outright, and every subagent type owes the same
		// reconciliation.
		hooks[event] = upsertEntry(eventEntries(root, event), isSteerOwned, entryValue(hookEntry{
			Hooks: []commandHook{{Type: "command", Command: resolver, Timeout: resolverTimeout}},
		}))
	}
	return path, save(path, root)
}

// entryValue renders one entry as the generic JSON value the file is edited as, so an
// unrelated key elsewhere in the document is never re-typed through a closed struct.
func entryValue(e hookEntry) any {
	raw, _ := json.Marshal(e)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// setOrDrop keeps an event key only while it holds something, so disarming the
// resolver does not leave an empty list behind.
func setOrDrop(hooks map[string]any, event string, entries []any) {
	if len(entries) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = entries
}

// RemoveCommitGate removes only this gate's handlers.
func RemoveCommitGate(scope, cwd string) (string, error) {
	return RemoveCommitGateAndResolver(scope, cwd)
}

// RemoveCommitGateAndResolver takes both steer-owned channels out in one save and
// leaves unrelated hooks alone.
func RemoveCommitGateAndResolver(scope, cwd string) (string, error) {
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
	setOrDrop(hooks, "PreToolUse", filterBy(preToolUse(root), isSteerOwned))
	for _, event := range resolverEvents {
		setOrDrop(hooks, event, filterBy(eventEntries(root, event), isSteerOwned))
	}
	return path, save(path, root)
}

// ResolverArming reports whether the resolver is registered on both turn-end events,
// and the timeout it carries.
func ResolverArming(scope, cwd string) (bool, int) {
	a := ResolverArmingRow(scope, cwd)
	return a.Registered, a.Timeout
}

// ResolverArmingRow reports the resolver's registration, its timeout and the command
// it names. The resolver rides a turn-end event, which takes no matcher.
func ResolverArmingRow(scope, cwd string) Arming {
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return Arming{}
	}
	a := Arming{Path: path}
	root, err := load(path)
	if err != nil {
		return a
	}
	timeout := 0
	command := ""
	for _, event := range resolverEvents {
		found := false
		for _, raw := range eventEntries(root, event) {
			entry, _ := raw.(map[string]any)
			hooks, _ := entry["hooks"].([]any)
			for _, rawHook := range hooks {
				hook, _ := rawHook.(map[string]any)
				cmdText, _ := hook["command"].(string)
				if !isResolver(cmdText) {
					continue
				}
				found = true
				command = cmdText
				if t, ok := hook["timeout"].(float64); ok {
					timeout = int(t)
				}
			}
		}
		if !found {
			return Arming{Path: path}
		}
	}
	a.Registered, a.Timeout, a.Command = true, timeout, command
	return a
}

// CommitGateArming reads registration and checks whether config.toml has a trust
// record at the handler's current index. It does not interpret trusted_hash.
func CommitGateArming(scope, cwd string) Arming {
	return armingFor(scope, cwd, isGate)
}

// armingFor finds the first PreToolUse handler this channel owns and reports its
// matcher, its timeout, and whether config.toml carries a trust record at the
// handler's current event/entry/handler index. Codex's handler hash algorithm is
// internal, so record presence is all Steer can honestly report.
func armingFor(scope, cwd string, owns func(string) bool) Arming {
	path, err := hooksPath(scope, cwd)
	if err != nil {
		return Arming{}
	}
	a := Arming{Path: path}
	root, err := load(path)
	if err != nil {
		return a
	}
	for i, raw := range preToolUse(root) {
		entry, _ := raw.(map[string]any)
		matcher, _ := entry["matcher"].(string)
		hooks, _ := entry["hooks"].([]any)
		for j, rawHook := range hooks {
			hook, _ := rawHook.(map[string]any)
			command, _ := hook["command"].(string)
			if !owns(command) {
				continue
			}
			a.Registered, a.Matcher, a.Command = true, matcher, command
			if timeout, ok := hook["timeout"].(float64); ok {
				a.Timeout = int(timeout)
			}
			if home, err := codexHome(); err == nil {
				a.TrustStateKnown = true
				config := filepath.Join(home, "config.toml")
				configRaw, readErr := os.ReadFile(config)
				if readErr != nil && !os.IsNotExist(readErr) {
					a.TrustStateKnown = false
					return a
				}
				key := fmt.Sprintf("%s:pre_tool_use:%d:%d", path, i, j)
				a.TrustRecordPresent = strings.Contains(string(configRaw), trustSection(key))
			}
			return a
		}
	}
	return a
}
