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

// AppendRule adds a rule under the Steer Rules section of the document at path,
// creating the section, and the file, when either is absent.
func AppendRule(path, rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return fmt.Errorf("rule cannot be empty")
	}

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

	updated, changed := appendRuleContent(content, rule)
	if !changed {
		return nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// HasRule reports whether the Steer Rules section of the document at path
// already carries this exact rule. A missing file carries nothing.
func HasRule(path, rule string) (bool, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	_, changed := appendRuleContent(string(data), rule)
	return !changed, nil
}

// RemoveRule removes an exact rule line from the Steer Rules section. A missing
// file, section or line is not an error.
func RemoveRule(path, rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	updated, changed := removeRuleContent(string(data), rule)
	if !changed {
		return nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
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
