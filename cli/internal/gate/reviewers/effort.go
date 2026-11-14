package reviewers

import (
	"fmt"
	"strings"
)

// EffortWarning returns the line to print when a provider does not document this
// effort, and "" when the value is documented, unset, or the provider declares no
// set. The value is passed through either way: a CLI can add a tier before Steer
// knows about it, and a list Steer refuses on would reject the new tier on
// release day.
func EffortWarning(provider, effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ""
	}
	p, ok := FindProvider(provider)
	if !ok || len(p.Efforts) == 0 {
		return ""
	}
	for _, v := range p.Efforts {
		if v == effort {
			return ""
		}
	}
	return fmt.Sprintf("reviewer effort %q is not one of %s's documented levels (%s); passing it through",
		effort, p.ID, strings.Join(p.Efforts, "|"))
}
