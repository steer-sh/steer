package cmd

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
)

var harnessFlag string
var allHarnesses bool

// registrarFor maps a registry id to its adapter. The guard store is shared, so
// an adapter only ever writes that harness's own registration and rules.
func registrarFor(id harness.ID) (harness.Registrar, error) {
	switch id {
	case harness.ClaudeCode:
		return claude.Registrar{}, nil
	case harness.Codex:
		return codex.Registrar{}, nil
	default:
		return nil, fmt.Errorf("harness %q registers no hooks", id)
	}
}

// currentRegistrars resolves the harnesses a mutation writes to: every harness
// under --all-harnesses, the named one under --harness, and otherwise the
// invoking harness. An ordinary shell sets no marker, and there the answer is
// Claude Code — the default every settings.json on disk already assumes.
func currentRegistrars() ([]harness.Registrar, error) {
	if allHarnesses {
		out := make([]harness.Registrar, 0, len(harness.All()))
		for _, entry := range harness.All() {
			if !entry.Capabilities.HookRegistration {
				continue
			}
			registrar, err := registrarFor(entry.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, registrar)
		}
		return out, nil
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
