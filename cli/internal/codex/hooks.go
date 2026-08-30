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
	CommitGateMatcher       = "exec_command"
	commitGateIdentity      = "commit-gate review-codex"
	CommitGateStatusMessage = "red-teaming the commit ..."
)

type commandHook struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
}

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

func preToolUse(root map[string]any) []any {
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["PreToolUse"].([]any)
	return entries
}

func isGate(command string) bool { return strings.Contains(command, commitGateIdentity) }

func filter(entries []any) []any {
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
			if !ok || !isGate(command) {
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

// RegisterCommitGate upserts one exec_command PreToolUse handler while preserving
// unrelated JSON keys at every level.
func RegisterCommitGate(scope, cwd, command string, timeout int) (string, error) {
	if !isGate(command) {
		return "", fmt.Errorf("commit gate command must contain %q", commitGateIdentity)
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
	entries := filter(preToolUse(root))
	entryRaw, _ := json.Marshal(hookEntry{Matcher: CommitGateMatcher, Hooks: []commandHook{{
		Type: "command", Command: command, Timeout: timeout, StatusMessage: CommitGateStatusMessage,
	}}})
	var entry any
	_ = json.Unmarshal(entryRaw, &entry)
	hooks["PreToolUse"] = append(entries, entry)
	return path, save(path, root)
}

// RemoveCommitGate removes only this gate's handlers.
func RemoveCommitGate(scope, cwd string) (string, error) {
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
	hooks["PreToolUse"] = filter(preToolUse(root))
	return path, save(path, root)
}

// CommitGateArming reads registration and checks whether config.toml has a trust
// record at the handler's current index. It does not interpret trusted_hash.
func CommitGateArming(scope, cwd string) Arming {
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
			if !isGate(command) {
				continue
			}
			a.Registered, a.Matcher = true, matcher
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
