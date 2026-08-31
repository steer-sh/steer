package report

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/cc"
	"github.com/steer-sh/steer-cli/internal/transcript/codex"
)

// Adapters returns one transcript adapter per harness that writes transcripts,
// in registry order. Restrict to one harness by passing its id; pass Unknown to
// mine every harness, which is the default for a read.
func Adapters(only harness.ID) []transcript.Adapter {
	var out []transcript.Adapter
	for _, h := range harness.All() {
		if only != harness.Unknown && h.ID != only {
			continue
		}
		switch h.ID {
		case harness.ClaudeCode:
			out = append(out, cc.New())
		case harness.Codex:
			out = append(out, codex.New())
		}
	}
	return out
}

// ProjectPaths returns the forms a harness may have recorded the working
// directory under. A session is keyed by whichever path it was launched from,
// so both the literal and the symlink-resolved path are offered.
func ProjectPaths() ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	paths := []string{cwd}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil && resolved != cwd {
		paths = append(paths, resolved)
	}
	return paths, nil
}

// Scan streams every discovered session of every adapter through one scanner
// and returns it with the projects it covered. A harness whose transcript root
// is unreadable contributes nothing and never fails the scan: the digest
// reports its coverage as zero rather than the run as an error.
func Scan(adapters []transcript.Adapter, since time.Time, filter transcript.Filter, onSkip func(transcript.Session, error)) (*Scanner, []string) {
	scanner := NewScanner(since)
	projects := map[string]struct{}{}

	for _, adapter := range adapters {
		root, err := adapter.Root()
		if err != nil {
			continue
		}
		sessions, err := adapter.Discover(root, filter)
		if err != nil {
			continue
		}
		// The harness is reachable, so it gets a coverage row now. A harness
		// with no sessions in the window must still report zeros; only an
		// unreadable root leaves it out of the digest entirely.
		scanner.Reached(adapter.Harness())
		for _, session := range sessions {
			if err := scanner.ScanSession(adapter, session); err != nil {
				if onSkip != nil {
					onSkip(session, err)
				}
				continue
			}
			projects[session.Project] = struct{}{}
		}
	}

	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names) // map order is random; sort so the digest is deterministic
	return scanner, names
}

// Generate runs the transcript scan end to end and returns the in-memory digest,
// without marshaling or size-capping it. The cadence runner reads it directly;
// the `steer report` command keeps its own scan because it also folds in
// instruction-file directives, which Generate omits — the cadence runner draws
// candidates from the transcript signal clusters, not from standing rules.
func Generate(windowDays int, allProjects bool) (*Digest, error) {
	projectPaths, err := ProjectPaths()
	if err != nil {
		return nil, err
	}

	since := time.Now().AddDate(0, 0, -windowDays)
	filter := transcript.Filter{Since: since, Projects: projectPaths, AllProjects: allProjects}
	scanner, names := Scan(Adapters(harness.Unknown), since, filter, nil)
	return scanner.Finalize(windowDays, names, nil), nil
}
