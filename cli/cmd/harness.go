package cmd

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
)

var harnessFlag string
var allHarnesses bool

// registrarFor maps a registry id to its adapter.
func registrarFor(id harness.ID) (harness.Registrar, error) { return guardscope.RegistrarFor(id) }

// currentRegistrars resolves the harnesses a mutation writes to: every harness
// that supports hooks under --all-harnesses, the named one under --harness, and
// otherwise every such harness installed on this machine. The guard store is
// shared, so a guard the operator installs is meant to run in each agent they
// actually use. A machine with no harness directory falls back to the invoking
// harness, and an ordinary shell sets no marker, so there the answer is Claude
// Code — the default every settings.json on disk already assumes.
func currentRegistrars() ([]harness.Registrar, error) {
	if allHarnesses {
		return guardscope.HookRegistrars(false)
	}
	if strings.TrimSpace(harnessFlag) == "" {
		installed, err := guardscope.HookRegistrars(true)
		if err != nil {
			return nil, err
		}
		if len(installed) > 0 {
			return installed, nil
		}
	}

	id, err := harness.ParseOrDetect(harnessFlag)
	if err != nil {
		return nil, err
	}
	if id == harness.Unknown {
		id = harness.ClaudeCode
	}
	registrar, err := registrarFor(id)
	if err != nil {
		return nil, err
	}
	return []harness.Registrar{registrar}, nil
}

// harnessNames renders the targets for the line a command prints after writing,
// so a Codex user reading "Claude Code" sees the mismatch and re-runs.
func harnessNames(targets []harness.Registrar) string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Harness().DisplayName)
	}
	return strings.Join(names, ", ")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&harnessFlag, "harness", "",
		"Harness to register into (claude-code|codex); defaults to the invoking harness")
	rootCmd.PersistentFlags().BoolVar(&allHarnesses, "all-harnesses", false,
		"Register into every harness that supports hooks")
}
