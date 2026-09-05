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

// executable names the CLI a provider runs. It mirrors each reviewer's own binary
// default, which a test may override on the struct and an operator never can.
func executable(provider string) string {
	switch canonicalProvider(provider) {
	case "agy":
		return "agy"
	case "codex":
		return "codex"
	case "claude-code":
		return "claude"
	}
	return ""
}

// ProbeProvider reports whether the reviewer CLI a provider names resolves here.
func ProbeProvider(provider string) Probe {
	name := executable(provider)
	if name == "" {
		return Probe{Provider: provider, State: ProbeUnknown}
	}
	if _, err := exec.LookPath(name); err != nil {
		return Probe{Provider: provider, Executable: name, State: ProbeMissing}
	}
	return Probe{Provider: provider, Executable: name, State: ProbeAvailable}
}
