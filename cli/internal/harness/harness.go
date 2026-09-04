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
	Pi         ID = "pi"
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
	// GlobalDirName overrides ConfigDirName for global scope. Empty means the
	// harness names one directory at both scopes.
	GlobalDirName string
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
		DetectEnv:       "CODEX_SESSION_ID",
		Capabilities: Capabilities{
			HookRegistration: true,
			Transcripts:      true,
			SubagentThreads:  true,
			TrustReview:      true,
		},
	},
	Pi: {
		ID:              Pi,
		DisplayName:     "Pi",
		InstructionFile: "AGENTS.md",
		ConfigDirName:   ".pi",
		GlobalDirName:   filepath.Join(".pi", "agent"),
		HomeEnv:         "PI_CODING_AGENT_DIR",
		DetectEnv:       "PI_CODING_AGENT",
		Capabilities: Capabilities{
			Transcripts: true,
			// Pi runs a project-local extension only once the operator trusts
			// it, so a registration will not prove enforcement here either.
			TrustReview: true,
		},
	},
}

// Registrar is what an adapter offers the guard lifecycle: it registers the one
// master hook in its harness's own configuration, and it owns that harness's
// instruction document. The guard store itself is neutral and shared, so nothing
// here names a file inside it.
type Registrar interface {
	// Harness names the registry entry this adapter serves.
	Harness() Harness
	// RegisterMaster upserts the master guard hook, leaving unrelated hooks alone.
	RegisterMaster(scope string) error
	// RemoveMaster takes the master guard hook back out.
	RemoveMaster(scope string) error
	// AppendRule adds a rule to the harness's instruction document.
	AppendRule(rule, scope string) error
	// RemoveRule takes one rule line back out.
	RemoveRule(rule, scope string) error
	// InstructionPath names the document AppendRule writes to, so a verifier can
	// read back what was written.
	InstructionPath(scope string) (string, error)
	// MasterRegistered reports whether this harness's configuration currently
	// invokes the master guard hook.
	MasterRegistered(scope string) (bool, error)
	// RegisteredMasterPath names the master file this harness's configuration
	// invokes, or "" when it invokes none. A migrated machine can hold a master
	// at both the legacy and the neutral path, so a reader asking about the
	// runtime in force has to ask the registration, never the store.
	RegisteredMasterPath(scope string) (string, error)
}

// Targets resolves a lifecycle target list. An empty list means the caller named
// no harness, and the answer is the fallback rather than nothing: a call that
// registered into Claude Code before Steer installed into a second harness must
// keep doing so.
func Targets(targets []Registrar, fallback Registrar) []Registrar {
	if len(targets) == 0 {
		return []Registrar{fallback}
	}
	return targets
}

// order fixes the iteration order of All, so every listing renders the same way.
var order = []ID{ClaudeCode, Codex, Pi}

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
// returns Unknown from an ordinary shell, and from a nested session where more
// than one marker is set: a child harness inherits its parent's environment, so
// two markers name a process whose harness cannot be read off the environment
// at all. STEER_HARNESS resolves that case.
func Detect() ID {
	if id := strings.TrimSpace(os.Getenv("STEER_HARNESS")); id != "" {
		if parsed, err := Parse(id); err == nil {
			return parsed
		}
	}
	found := Unknown
	for _, id := range order {
		env := registry[id].DetectEnv
		if env == "" || os.Getenv(env) == "" {
			continue
		}
		if found != Unknown {
			return Unknown
		}
		found = id
	}
	return found
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
		dir := h.GlobalDirName
		if dir == "" {
			dir = h.ConfigDirName
		}
		return filepath.Join(home, dir), nil
	default:
		return "", fmt.Errorf("invalid scope %q: expected %q or %q", scope, ProjectScope, GlobalScope)
	}
}
