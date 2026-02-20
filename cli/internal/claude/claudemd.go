package claude

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

// AppendRule adds a rule under the Steer Rules section in the scope-specific CLAUDE.md.
func AppendRule(rule, scope string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return fmt.Errorf("rule cannot be empty")
	}

	claudeMDPath, err := GetClaudeMDFile(scope)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(claudeMDPath), 0o755); err != nil && filepath.Dir(claudeMDPath) != "." {
		return fmt.Errorf("failed to create CLAUDE.md parent directory: %w", err)
	}

	content := ""
	if data, err := os.ReadFile(claudeMDPath); err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read CLAUDE.md: %w", err)
	}

	updated, changed := appendRuleContent(content, rule)
	if !changed {
		return nil
	}

	if err := os.WriteFile(claudeMDPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to write CLAUDE.md: %w", err)
	}

	return nil
}

// RemoveRule removes an exact rule line from the Steer Rules section.
func RemoveRule(rule, scope string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}

	claudeMDPath, err := GetClaudeMDFile(scope)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(claudeMDPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read CLAUDE.md: %w", err)
	}

	updated, changed := removeRuleContent(string(data), rule)
	if !changed {
		return nil
	}

	if err := os.WriteFile(claudeMDPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to write CLAUDE.md: %w", err)
	}

	return nil
}

func appendRuleContent(content, rule string) (string, bool) {
	trimmed := strings.TrimRight(content, "\n")
	lines := strings.Split(trimmed, "\n")
	if trimmed == "" {
		lines = []string{}
	}

	start, end := findSteerSection(lines)
	if start == -1 {
		if trimmed != "" {
			trimmed += "\n\n"
		}
		trimmed += steerRulesHeader + "\n"
		trimmed += managedByComment + "\n"
		trimmed += "- " + rule + "\n"
		return trimmed, true
	}

	ruleLine := "- " + rule
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) == ruleLine {
			return ensureTrailingNewline(strings.Join(lines, "\n")), false
		}
	}

	if start+1 >= len(lines) || strings.TrimSpace(lines[start+1]) != managedByComment {
		lines = insertLine(lines, start+1, managedByComment)
		if end >= start+1 {
			end++
		}
	}

	lines = insertLine(lines, end, ruleLine)
	return ensureTrailingNewline(strings.Join(lines, "\n")), true
}

func removeRuleContent(content, rule string) (string, bool) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return content, false
	}

	start, end := findSteerSection(lines)
	if start == -1 {
		return content, false
	}

	ruleLine := "- " + rule
	kept := make([]string, 0, len(lines))
	removed := false

	for i, line := range lines {
		if i > start && i < end && strings.TrimSpace(line) == ruleLine {
			removed = true
			continue
		}
		kept = append(kept, line)
	}

	if !removed {
		return content, false
	}

	return ensureTrailingNewline(strings.Join(kept, "\n")), true
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

func insertLine(lines []string, idx int, line string) []string {
	if idx < 0 {
		idx = 0
	}
	if idx > len(lines) {
		idx = len(lines)
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:idx]...)
	out = append(out, line)
	out = append(out, lines[idx:]...)
	return out
}

func ensureTrailingNewline(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}
