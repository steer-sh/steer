package claude

import (
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// ManifestRules returns the rules the manifest names, ordered by hook name so a
// second sync over identical state writes identical bytes.
func ManifestRules(scope string) ([]string, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(manifest.Hooks))
	for name := range manifest.Hooks {
		names = append(names, name)
	}
	sort.Strings(names)

	rules := make([]string, 0, len(names))
	for _, name := range names {
		if rule := strings.TrimSpace(manifest.Hooks[name].Rule); rule != "" {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// SyncTargets brings every target harness in line with the shared store: the
// master registration, and the rules section derived from the manifest. It names
// no hook, so it is also the retry for an install or a removal that stopped part
// way through the targets.
func SyncTargets(scope string, targets ...harness.Registrar) error {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return err
	}

	rules, err := ManifestRules(scope)
	if err != nil {
		return err
	}
	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return err
	}

	for _, target := range harness.Targets(targets, Registrar{}) {
		if len(hooks) == 0 {
			if err := target.RemoveMaster(scope); err != nil {
				return err
			}
		} else if err := target.RegisterMaster(scope); err != nil {
			return err
		}
		if err := target.SetRules(rules, scope); err != nil {
			return err
		}
	}
	return nil
}
