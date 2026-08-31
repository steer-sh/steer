// Package harness names the coding agents Steer installs into and describes what
// each one exposes. Core packages depend on this registry, never on a concrete
// harness adapter.
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ID string

const (
	// Unknown is the result of detection with no marker present. It is a valid
	// value: a mutation reaching it must name its target explicitly.
	Unknown    ID = ""
	ClaudeCode ID = "claude-code"
	Codex      ID = "codex"
)

const (
	ProjectScope = "project"
	GlobalScope  = "global"
)

// Capabilities records what a harness offers Steer. A false field means the
// harness has no equivalent event or evidence source.
type Capabilities struct {
	// HookRegistration: Steer can write hook registrations for this harness.
	HookRegistration bool
	// Transcripts: the harness writes session transcripts Steer can mine.
	Transcripts bool
	// SubagentThreads: transcripts carry subagent threads under a root session.
	SubagentThreads bool
	// PermissionDenied: the harness fires an event when its own classifier
	// blocks a tool call, which is what the denials corpus records.
	PermissionDenied bool
	// TrustReview: a hook stays inert until the operator trusts its current
	// definition, so registration alone does not prove enforcement.
	TrustReview bool
}

// Harness is one registry entry.
type Harness struct {
	ID          ID
	DisplayName string
	// InstructionFile is the per-repository instruction document Steer appends
	// its managed rules section to.
	InstructionFile string
	// ConfigDirName is the harness config directory, relative to a repository
	// root for project scope and to the user home for global scope.
	ConfigDirName string
	// HomeEnv overrides the global config directory when set in the
	// environment. Empty means the harness reads no such variable.
	HomeEnv string
	// DetectEnv is the environment variable a live session of this harness
	// sets. Empty means Steer has no verified marker for it.
	DetectEnv    string
	Capabilities Capabilities
}

var registry = map[ID]Harness{
	ClaudeCode: {
		ID:              ClaudeCode,
		DisplayName:     "Claude Code",
		InstructionFile: "CLAUDE.md",
		ConfigDirName:   ".claude",
		DetectEnv:       "CLAUDECODE",
		Capabilities: Capabilities{
			HookRegistration: true,
			Transcripts:      true,
			SubagentThreads:  true,
			PermissionDenied: true,
		},
	},
	Codex: {
		ID:              Codex,
		DisplayName:     "Codex",
		InstructionFile: "AGENTS.md",
		ConfigDirName:   ".codex",
		HomeEnv:         "CODEX_HOME",
		Capabilities: Capabilities{
			HookRegistration: true,
			Transcripts:      true,
			SubagentThreads:  true,
			TrustReview:      true,
		},
	},
}

// order fixes the iteration order of All, so every listing renders the same way.
var order = []ID{ClaudeCode, Codex}

// All returns every registered harness in a stable order.
func All() []Harness {
	out := make([]Harness, 0, len(order))
	for _, id := range order {
		out = append(out, registry[id])
	}
	return out
}

// Get returns the entry for id.
func Get(id ID) (Harness, bool) {
	h, ok := registry[id]
	return h, ok
}

// Parse normalizes an operator-supplied harness name. It accepts "claude" as an
// alias and reports an error for anything unregistered.
func Parse(name string) (ID, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "claude" {
		trimmed = string(ClaudeCode)
	}
	if _, ok := registry[ID(trimmed)]; ok {
		return ID(trimmed), nil
	}
	return Unknown, fmt.Errorf("unknown harness %q: expected %s", name, strings.Join(names(), " or "))
}

// ParseOrDetect resolves an explicit name, falling back to the invoking harness
// when name is empty.
func ParseOrDetect(name string) (ID, error) {
	if strings.TrimSpace(name) == "" {
		return Detect(), nil
	}
	return Parse(name)
}

func names() []string {
	out := make([]string, 0, len(order))
	for _, id := range order {
		out = append(out, string(id))
	}
	return out
}

// Detect names the harness running this process, from its session marker. It
// returns Unknown from an ordinary shell and from any harness whose marker
// Steer has not verified.
func Detect() ID {
	if id := strings.TrimSpace(os.Getenv("STEER_HARNESS")); id != "" {
		if parsed, err := Parse(id); err == nil {
			return parsed
		}
	}
	for _, id := range order {
		env := registry[id].DetectEnv
		if env != "" && os.Getenv(env) != "" {
			return id
		}
	}
	return Unknown
}

// ConfigDir resolves this harness's config directory for a scope. cwd is the
// repository root and is read for project scope only.
func (h Harness) ConfigDir(scope, cwd string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ProjectScope:
		return filepath.Join(cwd, h.ConfigDirName), nil
	case GlobalScope:
		if h.HomeEnv != "" {
			if configured := strings.TrimSpace(os.Getenv(h.HomeEnv)); configured != "" {
				return configured, nil
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, h.ConfigDirName), nil
	default:
		return "", fmt.Errorf("invalid scope %q: expected %q or %q", scope, ProjectScope, GlobalScope)
	}
}
