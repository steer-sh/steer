package reviewers

import "os/exec"

// Probe states. A probe reads PATH from THIS process, and the gate runs the reviewer
// from the harness's environment, whose PATH may differ. So a probe can prove an
// executable absent; it can never prove one runs. It is the same reading, and the
// same limit, as steerbin.Resolve over a registered hook command.
const (
	ProbeAvailable = "available"
	ProbeMissing   = "missing"
	ProbeUnknown   = "unknown"
)

// Probe is one provider's CLI and whether it resolves here.
type Probe struct {
	Provider   string
	Executable string
	State      string
}

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
