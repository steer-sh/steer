package claude

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// ManagedRule is one stored instruction rule, read back with the id that names it.
type ManagedRule struct {
	ID string
	RuleEntry
}

// InstallManagedRuleResult reports what an install replaced, so a caller can say
// whether it created a rule or reworded one.
type InstallManagedRuleResult struct {
	ID           string
	Overwritten  bool
	PreviousText string
}

// RemoveManagedRuleResult carries the text a removal took out of the document.
type RemoveManagedRuleResult struct {
	ID   string
	Text string
}

// InstallManagedRule writes one instruction rule into the shared store and
// re-derives every target's rules section.
//
// It does not consolidate. No guard changed, so rebuilding the master would clear
// every guard's tested state for an edit to prose. A store holding only rules
// registers no master either: ListIndividualHooks reads the store's guard files,
// so SyncTargets deregisters on its own.
func InstallManagedRule(scope, id, text string, targets ...harness.Registrar) (*InstallManagedRuleResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}

	ruleID, err := validateRuleID(id)
	if err != nil {
		return nil, err
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("rule text cannot be empty")
	}

	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	if _, taken := manifest.Hooks[ruleID]; taken {
		return nil, fmt.Errorf("%q already names a guard in the %s store. One id names one "+
			"artifact, or a record naming it cannot say which text its digest was taken over", ruleID, scope)
	}

	existing, overwritten := manifest.Rules[ruleID]
	now := time.Now().Format("2006-01-02")
	created := existing.Created
	if created == "" {
		created = now
	}

	entry := RuleEntry{
		Text:     text,
		RuleHash: HashRule(text),
		Created:  created,
		Updated:  now,
	}
	if err := SetRuleEntry(scope, ruleID, entry); err != nil {
		return nil, err
	}

	if err := SyncTargets(scope, targets...); err != nil {
		return nil, err
	}

	return &InstallManagedRuleResult{
		ID:           ruleID,
		Overwritten:  overwritten,
		PreviousText: existing.Text,
	}, nil
}

// RemoveManagedRule deletes one instruction rule and re-derives every target's
// rules section.
//
// The manifest entry goes before the targets loop, for the reason
// RemoveManagedHook removes a hook first: the store is shared, so one deletion
// has to stop the rule reaching every harness at once. A sync over an id the
// store no longer names is the retry for a removal that stopped part way through
// the targets, so it runs before the not-found answer.
func RemoveManagedRule(scope, id string, targets ...harness.Registrar) (*RemoveManagedRuleResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}

	ruleID, err := validateRuleID(id)
	if err != nil {
		return nil, err
	}

	if err := EnsureDirectories(scope); err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	entry, ok := manifest.Rules[ruleID]
	if !ok {
		if syncErr := SyncTargets(scope, targets...); syncErr != nil {
			return nil, syncErr
		}
		return nil, fmt.Errorf("rule not found: %s", id)
	}

	if err := RemoveRule(scope, ruleID); err != nil {
		return nil, err
	}
	if err := SyncTargets(scope, targets...); err != nil {
		return nil, err
	}

	return &RemoveManagedRuleResult{ID: ruleID, Text: entry.Text}, nil
}

// ListManagedRules reads one store's instruction rules, ordered by id.
func ListManagedRules(scope string) ([]ManagedRule, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	out := make([]ManagedRule, 0, len(manifest.Rules))
	for id, entry := range manifest.Rules {
		out = append(out, ManagedRule{ID: id, RuleEntry: entry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// validateRuleID accepts the ids a manifest key can carry and a person can retype.
// The id is what an intervention record names, so a value carrying whitespace or a
// path separator is refused rather than normalized into a neighbouring id.
func validateRuleID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("rule id cannot be empty")
	}
	if strings.ContainsAny(id, " \t\n/\\") {
		return "", fmt.Errorf("rule id %q carries whitespace or a path separator; use letters, digits, - and _", id)
	}
	return id, nil
}
