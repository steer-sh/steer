package reviewers

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Resolve builds the reviewer a provider name spells. An empty model means "the
// provider's own default", which is why agyDefault is handed to agy alone — it is a
// Gemini pin and names nothing on Codex or Claude Code.
func Resolve(provider, model, effort, agyDefault string) (gate.Reviewer, error) {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	switch canonicalProvider(provider) {
	case "agy":
		if model == "" {
			model = agyDefault
		}
		return NewAgy(model, effort), nil
	case "codex":
		return NewCodex(model, effort), nil
	case "claude-code":
		return NewClaudeCode(model, effort), nil
	default:
		return nil, fmt.Errorf("unknown reviewer provider %q: expected agy, codex or claude-code", provider)
	}
}
