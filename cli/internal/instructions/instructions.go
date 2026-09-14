// Package instructions edits the managed rules section of a harness's
// instruction document. Claude Code reads CLAUDE.md and Codex reads AGENTS.md;
// the section's grammar is the same in both, so it lives here once and each
// adapter supplies its own path.
package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	steerRulesHeader = "## Steer Rules"
	managedByComment = "<!-- managed-by-steer -->"
)

// SetRules replaces the Steer Rules section of the document at path with
// exactly these rules, creating the section, and the file, when either is
// absent. An empty set removes the section. A line written by hand inside the
// section does not survive; the section is Steer's, as the marker says.
func SetRules(path string, rules []string) error {
	wanted := normalizeRules(rules)

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create %s parent directory: %w", filepath.Base(path), err)
		}
	}

	content := ""
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	updated, changed := setRulesContent(content, wanted)
	if !changed {
		return nil
	}

	return writeAtomic(path, []byte(updated))
}

// HasRule reports whether the Steer Rules section of the document at path
// already carries this exact rule. A missing file carries nothing.
func HasRule(path, rule string) (bool, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return true, nil
	}
	carried, err := SectionRules(path)
	if err != nil {
		return false, err
	}
	for _, line := range carried {
		if line == rule {
			return true, nil
		}
	}
	return false, nil
}

// SectionRules returns the rules the Steer Rules section of the document at path
// carries, in document order. A missing file or section carries none. A line in
// the section that is not a bullet is not a rule and is skipped.
func SectionRules(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start, end := findSteerSection(lines)
	if start == -1 {
		return nil, nil
	}
	var out []string
	for i := start + 1; i < end; i++ {
		if rule, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "- "); ok {
			out = append(out, rule)
		}
	}
	return out, nil
}

func normalizeRules(rules []string) []string {
	out := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		out = append(out, rule)
	}
	return out
}

func sectionLines(rules []string) []string {
	lines := make([]string, 0, len(rules)+2)
	lines = append(lines, steerRulesHeader, managedByComment)
	for _, rule := range rules {
		lines = append(lines, "- "+rule)
	}
	return lines
}

func setRulesContent(content string, rules []string) (string, bool) {
	trimmed := strings.TrimRight(content, "\n")
	lines := strings.Split(trimmed, "\n")
	if trimmed == "" {
		lines = []string{}
	}

	start, end := findSteerSection(lines)

	var kept []string
	switch {
	case start == -1 && len(rules) == 0:
		return content, false
	case start == -1:
		kept = append(kept, lines...)
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, sectionLines(rules)...)
	default:
		kept = append(kept, lines[:start]...)
		if len(rules) > 0 {
			kept = append(kept, sectionLines(rules)...)
			if end < len(lines) {
				kept = append(kept, "")
			}
		} else {
			kept = trimTrailingBlanks(kept)
			if len(kept) > 0 && end < len(lines) {
				kept = append(kept, "")
			}
		}
		kept = append(kept, lines[end:]...)
	}

	updated := ensureTrailingNewline(strings.Join(kept, "\n"))
	if updated == content {
		return content, false
	}
	return updated, true
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// writeAtomic replaces path through a temporary file in the same directory, so a
// failed write leaves the document's own prose whole.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".steer-*")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func findSteerSection(lines []string) (int, int) {
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == steerRulesHeader {
			start = i
			break
		}
	}

	if start == -1 {
		return -1, -1
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}

	return start, end
}

func ensureTrailingNewline(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}
