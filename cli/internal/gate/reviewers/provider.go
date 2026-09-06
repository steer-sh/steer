package reviewers

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Provider is one reviewer CLI Steer can run. It is the one definition of the
// supported set: Resolve builds from it, EffortWarning reads its levels, and an
// editor offering a choice lists it rather than carrying its own copy.
type Provider struct {
	// ID is the value a setting stores and a flag names.
	ID string
	// Label is the CLI's name as a person writes it.
	Label string
	// Aliases are the other spellings a stored value may carry.
	Aliases []string
	// Efforts are the reasoning levels this CLI's own --help documents. An empty
	// list means the CLI documents none, so no value can be checked against it.
	Efforts []string
	// Executable is the command this provider runs, which a probe looks for.
	Executable string
	// build makes the reviewer. agyDefault is a model pin that names something on
	// agy alone, so every other provider ignores it.
	build func(model, effort, agyDefault string) gate.Reviewer
}

// providers is the supported set, in the order an editor lists it.
var providers = []Provider{
	{
		ID: "agy", Label: "agy", Aliases: []string{""}, Executable: "agy",
		Efforts: []string{"low", "medium", "high"},
		build: func(model, effort, agyDefault string) gate.Reviewer {
			if model == "" {
				model = agyDefault
			}
			return NewAgy(model, effort)
		},
	},
	{
		ID: "codex", Label: "Codex", Executable: "codex",
		build: func(model, effort, _ string) gate.Reviewer { return NewCodex(model, effort) },
	},
	{
		ID: "claude-code", Label: "Claude Code", Aliases: []string{"claude"},
		Executable: "claude",
		Efforts:    []string{"low", "medium", "high", "xhigh", "max"},
		build:      func(model, effort, _ string) gate.Reviewer { return NewClaudeCode(model, effort) },
	},
}

// Providers is the supported set.
func Providers() []Provider { return append([]Provider(nil), providers...) }

// FindProvider reads a stored or typed provider name, through its aliases. ok is
// false for a name no provider spells, which Resolve refuses and an editor lists
// the set for.
func FindProvider(name string) (Provider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range providers {
		if p.ID == name {
			return p, true
		}
		for _, alias := range p.Aliases {
			if alias == name {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// ProviderNames lists every ID, for an error message and for help text.
func ProviderNames() []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.ID)
	}
	return out
}

// ProviderLabel is a stored provider's name as a person writes it. An unknown
// value prints verbatim: it is what the file holds.
func ProviderLabel(name string) string {
	if p, ok := FindProvider(name); ok {
		return p.Label
	}
	return name
}
