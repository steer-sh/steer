package claude

import (
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// SetRules replaces the Steer Rules section of the scope-specific CLAUDE.md.
func SetRules(rules []string, scope string) error {
	path, err := GetClaudeMDFile(scope)
	if err != nil {
		return err
	}
	within, err := instructions.LinkBound(scope)
	if err != nil {
		return err
	}
	return instructions.SetRules(path, rules, within)
}
