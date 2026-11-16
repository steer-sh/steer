package reviewers

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Options is what a provider builds its reviewer from. A CLI provider reads only
// Model and Effort.
type Options struct {
	Model   string
	Effort  string
	BaseURL string
	KeyEnv  string
	// Tools is the API reviewer's tool mode, one of ToolModes; "" is off.
	Tools string
}

// ToolModes are the values reviewer.api.tools takes: off sends the prompt alone,
// auto offers the tools and falls back once when the endpoint refuses them, and
// required fails the run instead.
var ToolModes = []string{"off", "auto", "required"}

// Provider is one reviewer Steer can run. It is the one definition of the
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
	// Executable is the command this provider runs, which a probe looks for. It is
	// empty for a provider that sends to an endpoint.
	Executable string
	// API marks a provider that sends to an endpoint with a key, which a probe reads
	// from the configuration rather than PATH.
	API bool
	// validate refuses options the reviewer cannot run with. It is nil when every
	// option has a default.
	validate func(Options) error
	// build makes the reviewer from resolved options.
	build func(Options) gate.Reviewer
}

// APIProviderID is the provider that sends to an OpenAI-compatible endpoint.
const APIProviderID = "openai-compatible"

// providers is the supported set, in the order an editor lists it.
var providers = []Provider{
	{
		ID: "agy", Label: "Antigravity", Executable: "agy",
		Efforts: []string{"low", "medium", "high"},
		build:   func(o Options) gate.Reviewer { return NewAgy(o.Model, o.Effort) },
	},
	{
		ID: "codex", Label: "Codex", Executable: "codex",
		build: func(o Options) gate.Reviewer { return NewCodex(o.Model, o.Effort) },
	},
	{
		ID: "claude-code", Label: "Claude Code", Aliases: []string{"", "claude"},
		Executable: "claude",
		Efforts:    []string{"low", "medium", "high", "xhigh", "max"},
		build:      func(o Options) gate.Reviewer { return NewClaudeCode(o.Model, o.Effort) },
	},
	{
		ID: APIProviderID, Label: "OpenAI-compatible API", API: true,
		validate: validateChatAPI,
		build:    func(o Options) gate.Reviewer { return NewChatAPI(o) },
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
