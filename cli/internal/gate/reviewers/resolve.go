package reviewers

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Resolve builds the reviewer a provider name spells. An empty model takes the
// provider's entry in gate.DefaultReviewerModels, or the CLI's own default when the
// provider has none. A provider whose options are incomplete is refused here, so a
// gate never sends a request it cannot make.
func Resolve(provider string, opts Options) (gate.Reviewer, error) {
	p, ok := FindProvider(provider)
	if !ok {
		return nil, fmt.Errorf("unknown reviewer provider %q: expected %s",
			provider, strings.Join(ProviderNames(), ", "))
	}
	opts.Model = strings.TrimSpace(opts.Model)
	if opts.Model == "" {
		opts.Model = gate.DefaultReviewerModels[p.ID]
	}
	opts.Effort = strings.TrimSpace(opts.Effort)
	opts.BaseURL = strings.TrimSpace(opts.BaseURL)
	opts.KeyEnv = strings.TrimSpace(opts.KeyEnv)
	opts.Tools = strings.TrimSpace(opts.Tools)
	if p.validate != nil {
		if err := p.validate(opts); err != nil {
			return nil, err
		}
	}
	return p.build(opts), nil
}
