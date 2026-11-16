package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Approval states of a registered handler, read from config.toml.
const (
	// ApprovalApproved means the saved hash equals the handler's computed hash and
	// nothing turned the handler off. It says nothing about whether a hook ran.
	ApprovalApproved = "approved"
	// ApprovalDisabled means config.toml turns the handler off.
	ApprovalDisabled = "disabled"
	// ApprovalHooksOff means config.toml turns the whole hooks feature off.
	ApprovalHooksOff = "hooks-off"
	// ApprovalModified means a saved hash differs from the computed one. The handler
	// changed, or Codex hashes it another way; the two read the same from here.
	ApprovalModified = "modified"
	// ApprovalMissing means no saved hash exists for the handler.
	ApprovalMissing = "missing"
	// ApprovalUnverifiable means a saved hash exists for a handler whose shape this
	// comparison does not cover.
	ApprovalUnverifiable = "unverifiable"
	// ApprovalUnknown means config.toml could not be read.
	ApprovalUnknown = "unknown"
)

// defaultHookTimeout and defaultContextLimit are the values Codex normalizes an unset
// timeout to and drops from an additionalContextLimit before hashing.
const (
	defaultHookTimeout  = 600
	defaultContextLimit = 2500
)

// contextEvents are the registered events whose handlers keep additionalContextLimit
// in the hashed identity.
var contextEvents = map[string]bool{
	"PreToolUse": true, "PostToolUse": true, "SessionStart": true,
	"UserPromptSubmit": true, "SubagentStart": true,
}

// handlerSlot is one registered handler and where config.toml keys its approval.
type handlerSlot struct {
	event   string
	matcher string
	hook    map[string]any
	keys    []string
}

// slotAt builds the slot for the handler at an event's entry and handler index.
func slotAt(path, event string, entryIndex, handlerIndex int, entry, hook map[string]any) handlerSlot {
	matcher, _ := entry["matcher"].(string)
	return handlerSlot{event: event, matcher: matcher, hook: hook,
		keys: trustKeys(path, event, entryIndex, handlerIndex)}
}

// handlerHash computes the hash Codex saves for a handler: SHA-256 over compact JSON
// with sorted keys, of the event name, the matcher and the normalized handler. ok is
// false for a handler shape this comparison does not cover.
func handlerHash(s handlerSlot) (string, bool) {
	eventName, known := trustEvent[s.event]
	if !known {
		return "", false
	}
	if kind, _ := s.hook["type"].(string); kind != "command" {
		return "", false
	}
	command, _ := s.hook["command"].(string)
	if command == "" {
		return "", false
	}
	if async, _ := s.hook["async"].(bool); async {
		return "", false
	}
	timeout := defaultHookTimeout
	if raw, present := s.hook["timeout"]; present {
		value, isNumber := raw.(float64)
		if !isNumber {
			return "", false
		}
		timeout = max(int(value), 1)
	}
	handler := map[string]any{"type": "command", "command": command, "timeout": timeout, "async": false}
	if raw, present := s.hook["statusMessage"]; present {
		message, isText := raw.(string)
		if !isText {
			return "", false
		}
		handler["statusMessage"] = message
	}
	if raw, present := s.hook["additionalContextLimit"]; present && contextEvents[s.event] {
		limit, isNumber := raw.(float64)
		if !isNumber {
			return "", false
		}
		if int(limit) != defaultContextLimit {
			handler["additionalContextLimit"] = int(limit)
		}
	}
	identity := map[string]any{"event_name": eventName, "hooks": []any{handler}}
	if s.event != "Stop" && s.matcher != "" {
		identity["matcher"] = s.matcher
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(identity); err != nil {
		return "", false
	}
	sum := sha256.Sum256(bytes.TrimRight(out.Bytes(), "\n"))
	return "sha256:" + hex.EncodeToString(sum[:]), true
}

// approvalConfig is the part of config.toml approval depends on.
type approvalConfig struct {
	Features map[string]any `toml:"features"`
	Hooks    struct {
		State map[string]hookState `toml:"state"`
	} `toml:"hooks"`
}

// hookState is one handler's section under [hooks.state].
type hookState struct {
	TrustedHash string `toml:"trusted_hash"`
	Enabled     *bool  `toml:"enabled"`
}

// approvalOf reads every handler's approval and returns the state that most needs the
// operator, so one unapproved handler is not hidden by an approved one.
func approvalOf(slots []handlerSlot) string {
	home, err := codexHome()
	if err != nil {
		return ApprovalUnknown
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return ApprovalUnknown
	}
	var config approvalConfig
	if err := toml.Unmarshal(raw, &config); err != nil {
		return ApprovalUnknown
	}
	if config.Features["hooks"] == false || config.Features["codex_hooks"] == false {
		return ApprovalHooksOff
	}
	if len(slots) == 0 {
		return ApprovalMissing
	}
	// Codex keys every handler of one hooks.json under one path spelling. Steer cannot
	// tell which spelling Codex loads, so two that hold records and disagree stay
	// unanswered.
	answer := ""
	for spelling := range slots[0].keys {
		state, recorded := spellingApproval(slots, spelling, config.Hooks.State)
		switch {
		case !recorded:
		case answer == "":
			answer = state
		case answer != state:
			return ApprovalUnverifiable
		}
	}
	if answer == "" {
		return ApprovalMissing
	}
	return answer
}

// spellingApproval reads every handler under one key spelling. A handler with no
// section there is missing; recorded is false when none has one.
func spellingApproval(slots []handlerSlot, spelling int, states map[string]hookState) (state string, recorded bool) {
	found := map[string]bool{}
	for _, slot := range slots {
		if spelling >= len(slot.keys) {
			found[ApprovalMissing] = true
			continue
		}
		section, ok := states[slot.keys[spelling]]
		if !ok {
			found[ApprovalMissing] = true
			continue
		}
		recorded = true
		found[slotApproval(slot, section)] = true
	}
	for _, state := range []string{ApprovalDisabled, ApprovalMissing, ApprovalModified, ApprovalUnverifiable} {
		if found[state] {
			return state, recorded
		}
	}
	return ApprovalApproved, recorded
}

// slotApproval reads one handler's approval from its config.toml section.
func slotApproval(slot handlerSlot, section hookState) string {
	if section.Enabled != nil && !*section.Enabled {
		return ApprovalDisabled
	}
	if section.TrustedHash == "" {
		return ApprovalMissing
	}
	computed, supported := handlerHash(slot)
	switch {
	case !supported:
		return ApprovalUnverifiable
	case computed != section.TrustedHash:
		return ApprovalModified
	}
	return ApprovalApproved
}
