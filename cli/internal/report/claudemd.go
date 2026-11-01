package report

import (
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

const (
	maxDirectiveLen   = 200
	maxDirectivesFile = 150
)

// ExtractInstructionFile parses one harness instruction document — CLAUDE.md
// or AGENTS.md — into the lines Steer wrote and the directives it did not.
// Bullets in the Steer Rules section, over the range instructions.SectionRange
// gives, are Steer's; the ones whose text is in guardRules are also enforced by
// a guard, and the rest are instruction rules that no guard backs. Top-level bullets elsewhere are candidates for
// enforcement. A missing file yields an empty result, not an error.
func ExtractInstructionFile(id harness.ID, path string, guardRules []string) (InstructionFile, error) {
	out := InstructionFile{Harness: id, Path: path, SteerRules: []string{}, GuardRules: []string{}, Directives: []Directive{}}
	guarded := make(map[string]bool, len(guardRules))
	for _, rule := range guardRules {
		guarded[strings.TrimSpace(rule)] = true
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}

	lines := strings.Split(string(data), "\n")
	start, end := instructions.SectionRange(lines)
	section := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		inSteerRules := start < i && i < end
		if i == end {
			section = ""
		}

		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
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
		backed := inSteerRules && guarded[text]
		text = clip(text, maxDirectiveLen)

		if inSteerRules {
			out.SteerRules = append(out.SteerRules, text)
			if backed {
				out.GuardRules = append(out.GuardRules, text)
			}
		} else if len(out.Directives) < maxDirectivesFile {
			out.Directives = append(out.Directives, Directive{Section: section, Text: text})
		}
	}

	return out, nil
}
