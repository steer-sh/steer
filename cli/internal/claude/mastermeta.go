package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The constants the generated master declares. A reader finds them by name
// rather than by position, because the import block above them varies with the
// guards' own imports.
const (
	masterMetaConst  = "STEER_HOOK_META"
	masterBuildConst = "STEER_MASTER_BUILD_DIGEST"
)

// MasterHookMeta is one guard's identity as the registered master carries it.
// The digest is over the source the master embeds, so it names what runs rather
// than what the standalone guard file holds now.
type MasterHookMeta struct {
	HookName   string `json:"hook_name"`
	Rule       string `json:"rule"`
	HookDigest string `json:"hook_digest"`
	RuleHash   string `json:"rule_hash"`
}

// MasterIdentity is the implementation installed in one guard store.
type MasterIdentity struct {
	// BuildDigest is the master's own build stamp, which an audit row copies.
	BuildDigest string
	// Hooks is each guard's identity, by hook name.
	Hooks map[string]MasterHookMeta
}

// ReadMasterIdentity reads the registered master in a guard store directory.
//
// It reads the master rather than the manifest and the standalone guards: only
// the bytes the master embeds identify what ran. A store with no master yields
// an empty identity and no error, because a machine that installed no guard is
// not a failure to read one.
func ReadMasterIdentity(dir string) (MasterIdentity, error) {
	out := MasterIdentity{Hooks: map[string]MasterHookMeta{}}
	if dir == "" {
		return out, nil
	}
	path := filepath.Join(dir, MasterHookName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("failed to read %s: %w", path, err)
	}
	text := string(data)

	out.BuildDigest = pythonStringConst(text, masterBuildConst)

	object, ok := pythonDictConst(text, masterMetaConst)
	if !ok {
		return out, fmt.Errorf("%s declares no %s", path, masterMetaConst)
	}
	if err := json.Unmarshal([]byte(object), &out.Hooks); err != nil {
		return out, fmt.Errorf("failed to read %s from %s: %w", masterMetaConst, path, err)
	}
	return out, nil
}

// pythonStringConst returns the value of `NAME = "..."`, or "".
func pythonStringConst(text, name string) string {
	rest, ok := afterAssignment(text, name)
	if !ok || !strings.HasPrefix(rest, `"`) {
		return ""
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
}

// pythonDictConst returns the object literal assigned to name. The writer emits
// JSON, which is also a Python dict literal, so the span is scanned for its
// matching brace and handed to a JSON decoder.
func pythonDictConst(text, name string) (string, bool) {
	rest, ok := afterAssignment(text, name)
	if !ok || !strings.HasPrefix(rest, "{") {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return rest[:i+1], true
			}
		}
	}
	return "", false
}

// afterAssignment returns the text following `NAME = ` at the start of a line.
func afterAssignment(text, name string) (string, bool) {
	for _, prefix := range []string{name + " = ", name + "="} {
		idx := strings.Index(text, "\n"+prefix)
		if idx < 0 {
			if strings.HasPrefix(text, prefix) {
				return text[len(prefix):], true
			}
			continue
		}
		return text[idx+1+len(prefix):], true
	}
	return "", false
}
