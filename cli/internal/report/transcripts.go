package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptFile is one session transcript eligible for scanning.
type TranscriptFile struct {
	Path       string
	ProjectDir string // munged directory name under ~/.claude/projects
}

// ProjectsDir returns the Claude Code transcript root (~/.claude/projects).
func ProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// normalizeForMatch collapses a path or munged directory name to a canonical
// form for comparison: every non-alphanumeric byte becomes '-'. Claude Code
// munges project paths this way when naming transcript directories, but the
// exact character set it replaces is undocumented — normalizing both sides
// makes the comparison robust to that.
func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// FindTranscripts lists transcript files modified at or after `since`.
// When allProjects is false, only directories matching one of projectPaths
// (munged) are included. Callers pass both the literal working directory and
// its symlink-resolved form: Claude Code names transcript directories after
// whichever path the session was launched from.
func FindTranscripts(projectsDir string, projectPaths []string, allProjects bool, since time.Time) ([]TranscriptFile, error) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", projectsDir, err)
	}

	want := map[string]struct{}{}
	for _, p := range projectPaths {
		want[normalizeForMatch(p)] = struct{}{}
	}

	var files []TranscriptFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !allProjects {
			if _, ok := want[normalizeForMatch(entry.Name())]; !ok {
				continue
			}
		}

		dir := filepath.Join(projectsDir, entry.Name())
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".jsonl") {
				continue
			}
			info, err := item.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(since) {
				continue
			}
			files = append(files, TranscriptFile{
				Path:       filepath.Join(dir, item.Name()),
				ProjectDir: entry.Name(),
			})
		}
	}

	return files, nil
}
