package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// statusLineKey is the settings.json top-level key. It rides Settings.extra, so a
// file steer never installed into round-trips it untouched.
const statusLineKey = "statusLine"

// statusLineIdentity recognises steer's own status line command. The subcommand has
// to sit as its own argument: a user's ~/.claude/statusline.sh is not steer's, and
// reading it as steer's would let install overwrite it without asking.
var statusLineIdentity = regexp.MustCompile(`(^|\s)statusline(\s|$)`)

// WrapFlag carries the command steer wraps. The value is base64 so removal recovers
// the original byte for byte without a shell parser.
const WrapFlag = "--wrap-b64"

// StatusLineEntry models the statusLine object. Only type and command are modeled;
// padding, refreshInterval and hideVimModeIndicator ride extra, so an install
// changes the command and keeps what the operator set.
type StatusLineEntry struct {
	Type    string
	Command string

	extra map[string]json.RawMessage
}

func (e *StatusLineEntry) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, target := range map[string]any{"type": &e.Type, "command": &e.Command} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(value, target); err != nil {
			return err
		}
		delete(raw, key)
	}
	e.extra = raw
	return nil
}

func (e StatusLineEntry) MarshalJSON() ([]byte, error) {
	raw := map[string]json.RawMessage{}
	for key, value := range e.extra {
		raw[key] = value
	}
	for key, value := range map[string]string{"type": e.Type, "command": e.Command} {
		if value == "" {
			delete(raw, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw[key] = encoded
	}
	return json.Marshal(raw)
}

// IsStatusLineCommand reports whether a command is steer's status line. The head has
// to BE steer, not merely mention it: a foreign command carrying steer in an argument
// would otherwise be overwritten by install and deleted by remove.
func IsStatusLineCommand(command string) bool {
	return steerbin.IsSteerCommand(command) && statusLineIdentity.MatchString(command)
}

// WrappedCommand returns the command a steer status line wraps, and whether it
// carries one.
func WrappedCommand(command string) (string, bool) {
	_, rest, found := strings.Cut(command, WrapFlag+" ")
	if !found {
		return "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(fields[0])
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// EncodeWrapped renders the flag that carries a wrapped command.
func EncodeWrapped(command string) string {
	return WrapFlag + " " + base64.StdEncoding.EncodeToString([]byte(command))
}

// StatusLine returns the entry the settings carry, and whether the key is present.
func (s *Settings) StatusLine() (StatusLineEntry, bool) {
	raw, ok := s.extra[statusLineKey]
	if !ok {
		return StatusLineEntry{}, false
	}
	var entry StatusLineEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return StatusLineEntry{}, false
	}
	return entry, true
}

// SetStatusLine writes the entry, replacing any key already there.
func (s *Settings) SetStatusLine(entry StatusLineEntry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if s.extra == nil {
		s.extra = map[string]json.RawMessage{}
	}
	s.extra[statusLineKey] = encoded
	return nil
}

// ClearStatusLine removes the key entirely, leaving the operator with no status line.
func (s *Settings) ClearStatusLine() {
	delete(s.extra, statusLineKey)
}

// StatusLineArmed returns the scope's entry and whether it is steer's.
func StatusLineArmed(scope string) (StatusLineEntry, bool, bool) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return StatusLineEntry{}, false, false
	}
	entry, present := settings.StatusLine()
	return entry, present, present && IsStatusLineCommand(entry.Command)
}

// RegisterStatusLine writes steer's status line into the scope. It keeps the
// sibling keys of an entry already there — padding and refreshInterval are the
// operator's, not the command's.
func RegisterStatusLine(scope, command string) error {
	if !IsStatusLineCommand(command) {
		return fmt.Errorf("refusing to register %q as the status line: the command must "+
			"name steer and carry the statusline subcommand, or the entry cannot be "+
			"recognised again", command)
	}
	settings, err := LoadSettings(scope)
	if err != nil {
		return err
	}
	entry, _ := settings.StatusLine()
	entry.Type = "command"
	entry.Command = command
	if err := settings.SetStatusLine(entry); err != nil {
		return err
	}
	return SaveSettings(scope, settings)
}

// RemoveStatusLine takes steer's status line out. An entry wrapping a command
// restores that command in place; one that wraps nothing clears the key. An entry
// that is not steer's is left alone and reported.
func RemoveStatusLine(scope string) (restored string, err error) {
	settings, err := LoadSettings(scope)
	if err != nil {
		return "", err
	}
	entry, present := settings.StatusLine()
	if !present {
		return "", nil
	}
	if !IsStatusLineCommand(entry.Command) {
		return "", fmt.Errorf("the status line runs %q, which steer did not install: "+
			"remove it by hand or leave it", entry.Command)
	}
	if wrapped, ok := WrappedCommand(entry.Command); ok {
		entry.Command = wrapped
		if err := settings.SetStatusLine(entry); err != nil {
			return "", err
		}
		return wrapped, SaveSettings(scope, settings)
	}
	settings.ClearStatusLine()
	return "", SaveSettings(scope, settings)
}
