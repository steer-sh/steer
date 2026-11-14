package cmd

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
)

var harnessFlag string
var allHarnesses bool

// registrarFor maps a registry id to its adapter.
func registrarFor(id harness.ID) (harness.Registrar, error) { return guardscope.RegistrarFor(id) }

// currentRegistrars resolves the harnesses a mutation in scope writes to: the ones
// --harness or --all-harnesses names, and otherwise status.GuardTargets of the
// installed ones, so a harness Steer was removed from is not registered again.
func currentRegistrars(scope string) ([]harness.Registrar, error) {
	candidates, err := candidateRegistrars()
	if err != nil || harnessNamed() {
		return candidates, err
	}
	return status.GuardTargets(scope, candidates)
}

// candidateRegistrars are the harnesses a command may write to before
// status.GuardTargets narrows them: the named ones, or every installed one, or the
// invoking harness on a machine with none installed; an ordinary shell sets no
// marker, so there it is Claude Code, the default every settings.json assumes.
func candidateRegistrars() ([]harness.Registrar, error) {
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

// harnessNamed reports whether the user chose the targets with --harness or
// --all-harnesses.
func harnessNamed() bool {
	return allHarnesses || strings.TrimSpace(harnessFlag) != ""
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
		"Harness to register into (claude-code|codex); defaults to every installed harness, "+
			"or in the global scope to those that hold a Steer registration when any does")
	rootCmd.PersistentFlags().BoolVar(&allHarnesses, "all-harnesses", false,
		"Register into every harness that supports hooks")
}
