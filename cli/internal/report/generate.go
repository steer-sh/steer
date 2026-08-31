package report

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/cc"
)

// Generate runs the transcript scan end to end and returns the in-memory digest,
// without marshaling or size-capping it. The cadence runner reads it directly;
// the `steer report` command keeps its own scan because it also folds in CLAUDE.md
// directives, which Generate omits — the cadence runner draws candidates from the
// transcript signal clusters, not from standing rules.
func Generate(windowDays int, allProjects bool) (*Digest, error) {
	adapter := cc.New()
	root, err := adapter.Root()
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
	filter := transcript.Filter{Since: since, Projects: projectPaths, AllProjects: allProjects}
	sessions, err := adapter.Discover(root, filter)
	if err != nil {
		return nil, err
	}

	scanner := NewScanner(since)
	projects := map[string]struct{}{}
	for _, session := range sessions {
		if err := scanner.ScanSession(adapter, session); err != nil {
			continue
		}
		projects[session.Project] = struct{}{}
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names) // map order is random; sort so the digest is deterministic
	return scanner.Finalize(windowDays, names, nil), nil
}
