package reviewers

import (
	"os/exec"
	"strings"

	"github.com/steer-sh/steer-cli/internal/apikey"
)

// Probe states. A probe reads PATH from THIS process, and the gate runs the reviewer
// from the harness's environment, whose PATH may differ. So a probe can prove an
// executable absent; it can never prove one runs. It is the same reading, and the
// same limit, as steerbin.Resolve over a registered hook command.
const (
	ProbeAvailable = "available"
	ProbeMissing   = "missing"
	ProbeUnknown   = "unknown"
)

// Probe is one provider's CLI and whether it resolves here. For an API provider it
// is whether the configuration and the key are in place; it sends no request.
type Probe struct {
	Provider   string
	Executable string
	State      string
	// Unset names what a missing API provider lacks: a setting name, or UnsetKey.
	Unset string
	// Detail says why, in words, for an API provider that is missing.
	Detail string
	// Key is where an API provider's key comes from, "" for a CLI provider.
	Key string
}

// UnsetKey is a Probe's Unset when only the API key is missing.
const UnsetKey = "key"

// Available reports whether the reviewer can run. An unknown provider is not a claim
// of absence: nothing was checked.
func (p Probe) Available() bool { return p.State != ProbeMissing }

// ProbeProvider reports whether the reviewer CLI a provider names resolves here.
func ProbeProvider(provider string) Probe {
	// The name mirrors each reviewer's own binary default, which a test may
	// override on the struct and an operator never can.
	p, ok := FindProvider(provider)
	if !ok || p.Executable == "" {
		return Probe{Provider: provider, State: ProbeUnknown}
	}
	name := p.Executable
	if _, err := exec.LookPath(name); err != nil {
		return Probe{Provider: provider, Executable: name, State: ProbeMissing}
	}
	return Probe{Provider: provider, Executable: name, State: ProbeAvailable}
}

// ProbeConfig is ProbeProvider for a provider that reads its configuration: an API
// provider is available when its options validate and a key resolves in this
// process. The gate reads the key in the harness's process, whose environment may
// differ, so an available reading is not proof that the gate finds the key.
func ProbeConfig(provider string, opts Options) Probe {
	p, ok := FindProvider(provider)
	if !ok || !p.API {
		return ProbeProvider(provider)
	}
	probe := Probe{Provider: provider, State: ProbeMissing}
	opts.Model = strings.TrimSpace(opts.Model)
	opts.BaseURL = strings.TrimSpace(opts.BaseURL)
	if err := p.validate(opts); err != nil {
		probe.Detail = err.Error()
		switch {
		case opts.BaseURL == "" || CheckBaseURL(opts.BaseURL) != nil:
			probe.Unset = "reviewer.api.base_url"
		default:
			probe.Unset = "reviewer.model"
		}
		return probe
	}
	key, status := findKey(opts.KeyEnv)
	probe.Key = status.Label()
	if key == "" {
		probe.Unset = UnsetKey
		probe.Detail = status.Missing()
		return probe
	}
	probe.State = ProbeAvailable
	return probe
}

// findKey is apikey.Find, a variable so a test reads no operator file.
var findKey = apikey.Find
