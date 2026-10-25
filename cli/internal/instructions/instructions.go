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

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const (
	steerRulesHeader = "## Steer Rules"
	managedByComment = "<!-- managed-by-steer -->"
	// EndMarker closes the Steer Rules section; every line after it is the user's.
	EndMarker = "<!-- end-managed-by-steer -->"
)

// SetRules replaces the Steer Rules section of the document at path with
// exactly these rules, creating the section, and the file, when either is
// absent. An empty set removes the section. A line written by hand inside the
// section does not survive; the section is Steer's, as the marker says. A
// section without EndMarker is rewritten only when it holds nothing but blank
// lines, the marker and bullets, and no EndMarker follows the heading that ends
// it; otherwise the document is left as it was and the error names the line. A
// symlinked document is written at its target, so the link survives. within,
// when not empty, bounds that target: a link that resolves outside it is
// refused. steerpath.LinkBound names the bound for a scope.
func SetRules(path string, rules []string, within string) error {
	wanted := normalizeRules(rules)

	path, err := resolveLink(path, within)
	if err != nil {
		return err
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

	updated, changed, err := setRulesContent(content, wanted)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
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
	start, end, _, _ := findSteerSection(lines)
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
	lines := make([]string, 0, len(rules)+3)
	lines = append(lines, steerRulesHeader, managedByComment)
	for _, rule := range rules {
		lines = append(lines, "- "+rule)
	}
	return append(lines, EndMarker)
}

func setRulesContent(content string, rules []string) (string, bool, error) {
	trimmed := strings.TrimRight(content, "\n")
	lines := strings.Split(trimmed, "\n")
	if trimmed == "" {
		lines = []string{}
	}

	start, end, closed, stray := findSteerSection(lines)
	if stray != -1 {
		return "", false, fmt.Errorf("line %d holds %s past the heading on line %d, which ends the %s section "+
			"that has no marker of its own; move the marker to where the section should end, or remove it",
			stray+1, EndMarker, end+1, steerRulesHeader)
	}
	if start != -1 && !closed {
		for i := start + 1; i < end; i++ {
			if !legacySectionLine(lines[i]) {
				return "", false, fmt.Errorf("the %s section has no %s and line %d is not one Steer wrote: %q; "+
					"insert %s where the section should end", steerRulesHeader, EndMarker, i+1, lines[i], EndMarker)
			}
		}
	}

	var kept []string
	switch {
	case start == -1 && len(rules) == 0:
		return content, false, nil
	case start == -1:
		kept = append(kept, lines...)
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, sectionLines(rules)...)
	default:
		rest := lines[end:]
		for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
			rest = rest[1:]
		}
		kept = append(kept, lines[:start]...)
		if len(rules) > 0 {
			kept = append(kept, sectionLines(rules)...)
		} else {
			kept = trimTrailingBlanks(kept)
		}
		if len(kept) > 0 && len(rest) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, rest...)
	}

	updated := ensureTrailingNewline(strings.Join(kept, "\n"))
	if updated == content {
		return content, false, nil
	}
	return updated, true, nil
}

// legacySectionLine reports a line a section written before EndMarker can hold.
func legacySectionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || trimmed == managedByComment || strings.HasPrefix(trimmed, "- ")
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// resolveLink returns the file a symlinked path names, or path itself when it is
// no symlink or does not exist. A symlink to a missing file is an error, and so
// is one that resolves outside within when within is not empty.
func resolveLink(path, within string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink that does not resolve: %w", path, err)
	}
	if within != "" && !steerpath.Within(within, target) {
		return "", fmt.Errorf("%s links to %s, outside %s; Steer writes a project's instruction document only inside its repository", path, target, within)
	}
	return target, nil
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

// SectionRange returns the line range [start, end) of the Steer Rules section in
// a document split on "\n", or -1, -1. It is the boundary SetRules writes to.
func SectionRange(lines []string) (start, end int) {
	start, end, _, _ = findSteerSection(lines)
	return start, end
}

// findSteerSection returns the line range [start, end) of the Steer Rules
// section, or -1, -1. A closed section ends after its EndMarker. One without it
// ends at the next level-1 or level-2 heading or second Steer Rules heading, or
// at the end of the document; stray is then the line of an EndMarker past that
// heading and before any second Steer Rules heading, or -1.
func findSteerSection(lines []string) (start, end int, closed bool, stray int) {
	start, end, stray = -1, len(lines), -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case start == -1:
			if trimmed == steerRulesHeader {
				start = i
			}
		case trimmed == steerRulesHeader:
			if end == len(lines) {
				end = i
			}
			return start, end, false, stray
		case trimmed == EndMarker:
			if end == len(lines) {
				return start, i + 1, true, -1
			}
			if stray == -1 {
				stray = i
			}
		case end == len(lines) && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")):
			end = i
		}
	}
	if start == -1 {
		return -1, -1, false, -1
	}
	return start, end, false, stray
}

func ensureTrailingNewline(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}
