package report

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Generate runs the transcript scan end to end and returns the in-memory digest,
// without marshaling or size-capping it. The cadence runner reads it directly;
// the `steer report` command keeps its own scan because it also folds in CLAUDE.md
// directives, which Generate omits — the cadence runner draws candidates from the
// transcript signal clusters, not from standing rules.
func Generate(windowDays int, allProjects bool) (*Digest, error) {
	projectsDir, err := ProjectsDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	projectPaths := []string{cwd}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil && resolved != cwd {
		projectPaths = append(projectPaths, resolved)
	}

	since := time.Now().AddDate(0, 0, -windowDays)
	files, err := FindTranscripts(projectsDir, projectPaths, allProjects, since)
	if err != nil {
		return nil, err
	}

	scanner := NewScanner(since)
	projects := map[string]struct{}{}
	for _, f := range files {
		if err := scanner.ScanFile(f); err != nil {
			continue
		}
		projects[f.ProjectDir] = struct{}{}
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names) // map order is random; sort so the digest is deterministic
	return scanner.Finalize(windowDays, names, nil), nil
}
