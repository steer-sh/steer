package reviewers

import (
	"fmt"
	"strings"
)

// knownEfforts is the set each provider's own --help documents. Codex is absent on
// purpose: it takes effort as a `model_reasoning_effort` config override, whose
// accepted values are the model's rather than the CLI's, so Steer knows no set to
// check against and warns about nothing.
var knownEfforts = map[string][]string{
	"agy":         {"low", "medium", "high"},
	"claude-code": {"low", "medium", "high", "xhigh", "max"},
}

// EffortWarning returns the line to print when provider does not document effort, and
// "" when the value is documented, unset, or the provider declares no set. The value
// is passed through either way: a CLI can add a tier before Steer knows about it, and
// a list Steer refuses on would then reject the new tier on release day.
func EffortWarning(provider, effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ""
	}
	name := canonicalProvider(provider)
	known, ok := knownEfforts[name]
	if !ok {
		return ""
	}
	for _, v := range known {
		if v == effort {
			return ""
		}
	}
	return fmt.Sprintf("reviewer effort %q is not one of %s's documented levels (%s); passing it through",
		effort, name, strings.Join(known, "|"))
}

// canonicalProvider maps the spellings buildReviewer accepts onto one key.
func canonicalProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "agy":
		return "agy"
	case "claude", "claude-code":
		return "claude-code"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}
