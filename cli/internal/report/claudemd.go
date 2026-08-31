package report

import (
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
)

const (
	maxDirectiveLen   = 200
	maxDirectivesFile = 150
	steerRulesHeader  = "## Steer Rules"
)

// ExtractInstructionFile parses one harness instruction document — CLAUDE.md
// or AGENTS.md — into steer-enforced rules and unenforced directives. Bullets
// under "## Steer Rules" are already backed by hooks; top-level bullets
// elsewhere are candidates for enforcement. A missing file yields an empty
// result, not an error.
func ExtractInstructionFile(id harness.ID, path string) (InstructionFile, error) {
	out := InstructionFile{Harness: id, Path: path, SteerRules: []string{}, Directives: []Directive{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}

	section := ""
	inSteerRules := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
			inSteerRules = trimmed == strings.TrimSpace(steerRulesHeader)
			section = strings.TrimLeft(trimmed, "# ")
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			section = strings.TrimLeft(trimmed, "# ")
			continue
		}

		// Top-level bullets only: nested bullets are elaboration, not rules.
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if text == "" {
			continue
		}
		if len(text) > maxDirectiveLen {
			text = text[:maxDirectiveLen]
		}

		if inSteerRules {
			out.SteerRules = append(out.SteerRules, text)
		} else if len(out.Directives) < maxDirectivesFile {
			out.Directives = append(out.Directives, Directive{Section: section, Text: text})
		}
	}

	return out, nil
}
