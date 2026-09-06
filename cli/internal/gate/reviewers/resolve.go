package reviewers

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Resolve builds the reviewer a provider name spells. An empty model means "the
// provider's own default", which is why agyDefault reaches agy alone — it is a
// Gemini pin and names nothing on Codex or Claude Code.
func Resolve(provider, model, effort, agyDefault string) (gate.Reviewer, error) {
	p, ok := FindProvider(provider)
	if !ok {
		return nil, fmt.Errorf("unknown reviewer provider %q: expected %s",
			provider, strings.Join(ProviderNames(), ", "))
	}
	return p.build(strings.TrimSpace(model), strings.TrimSpace(effort), agyDefault), nil
}
