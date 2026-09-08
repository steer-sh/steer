package claude

import (
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// ManifestRules returns the rules the manifest names, ordered by the id that
// names each one so a second sync over identical state writes identical bytes.
//
// The manifest has two sources. A hook's rule is the prose behind a guard; a
// rules entry is an instruction-tier rule with no guard behind it. The derived
// section cannot tell them apart, and that is the point: both are one bullet the
// agent reads. An id naming both is refused when the rule is written, so sorting
// on the id alone is total.
func ManifestRules(scope string) ([]string, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	type entry struct{ id, text string }
	entries := make([]entry, 0, len(manifest.Hooks)+len(manifest.Rules))
	for id, hook := range manifest.Hooks {
		entries = append(entries, entry{id, hook.Rule})
	}
	for id, rule := range manifest.Rules {
		entries = append(entries, entry{id, rule.Text})
	}
	// Sorted on the id, then the text. Both write boundaries refuse an id naming a
	// guard and a rule, so only a hand-edited manifest reaches a collision here; it
	// yields both lines rather than losing one, and the second key keeps the order
	// total whichever map the reader ranged first.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].id != entries[j].id {
			return entries[i].id < entries[j].id
		}
		return entries[i].text < entries[j].text
	})

	rules := make([]string, 0, len(entries))
	for _, e := range entries {
		if rule := strings.TrimSpace(e.text); rule != "" {
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
