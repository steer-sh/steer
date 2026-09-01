package claude

import (
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// AppendRule adds a rule under the Steer Rules section in the scope-specific CLAUDE.md.
func AppendRule(rule, scope string) error {
	path, err := GetClaudeMDFile(scope)
	if err != nil {
		return err
	}
	return instructions.AppendRule(path, rule)
}

// RemoveRule removes an exact rule line from the Steer Rules section.
func RemoveRule(rule, scope string) error {
	path, err := GetClaudeMDFile(scope)
	if err != nil {
		return err
	}
	return instructions.RemoveRule(path, rule)
}
