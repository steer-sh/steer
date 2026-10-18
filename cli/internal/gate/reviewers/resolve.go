package reviewers

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Resolve builds the reviewer a provider name spells. An empty model takes the
// provider's entry in gate.DefaultReviewerModels, or the CLI's own default when the
// provider has none.
func Resolve(provider, model, effort string) (gate.Reviewer, error) {
	p, ok := FindProvider(provider)
	if !ok {
		return nil, fmt.Errorf("unknown reviewer provider %q: expected %s",
			provider, strings.Join(ProviderNames(), ", "))
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = gate.DefaultReviewerModels[p.ID]
	}
	return p.build(model, strings.TrimSpace(effort)), nil
}
