package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// coveragePrefix names the per-host coverage records, beside the shards.
const coveragePrefix = "coverage"

// Coverage is what the last complete build of one host's shard read: when it ran,
// how far back, and which sessions its scope selected. A reader of stored signals
// tests it before trusting their absence.
type Coverage struct {
	ScannedAt  time.Time `json:"scanned_at"`
	WindowDays int       `json:"window_days"`
	// Harness is the one harness the build read, empty when it read every harness.
	Harness     string   `json:"harness,omitempty"`
	AllProjects bool     `json:"all_projects,omitempty"`
	Projects    []string `json:"projects,omitempty"`
	RepoRoot    string   `json:"repo_root,omitempty"`
}

// CoveragePath is the coverage record this machine writes.
func CoveragePath(dir, host string) string {
	return filepath.Join(dir, coveragePrefix+"."+host+".json")
}

func coverageOf(f transcript.Filter, now time.Time, windowDays int, only harness.ID) Coverage {
	c := Coverage{ScannedAt: now.UTC(), WindowDays: windowDays, AllProjects: f.AllProjects, RepoRoot: f.RepoRoot}
	if only != harness.Unknown {
		c.Harness = string(only)
	}
	if !f.AllProjects && f.RepoRoot == "" {
		for _, p := range f.Projects {
			c.Projects = append(c.Projects, transcript.NormalizeProject(p))
		}
		sort.Strings(c.Projects)
	}
	return c
}

// LoadCoverage reads one host's coverage record. It is false when the record is
// missing or unreadable, which no stored signal can stand in for.
func LoadCoverage(dir, host string) (Coverage, bool) {
	data, err := os.ReadFile(CoveragePath(dir, host))
	if err != nil {
		return Coverage{}, false
	}
	var c Coverage
	if json.Unmarshal(data, &c) != nil || c.ScannedAt.IsZero() {
		return Coverage{}, false
	}
	return c, true
}

// Covers reports that the build read every harness, at least windowDays back from
// when it ran, over a scope that selects every session f selects, and that it ran
// inside the window that ends now.
func (c Coverage) Covers(f transcript.Filter, windowDays int, now time.Time) bool {
	if c.Harness != "" || c.WindowDays < windowDays || c.ScannedAt.Before(now.AddDate(0, 0, -windowDays)) {
		return false
	}
	switch {
	case c.AllProjects:
		return true
	case c.RepoRoot != "":
		return f.RepoRoot == c.RepoRoot
	case f.AllProjects || f.RepoRoot != "" || len(f.Projects) == 0:
		return false
	}
	have := map[string]bool{}
	for _, p := range c.Projects {
		have[p] = true
	}
	for _, p := range f.Projects {
		if !have[transcript.NormalizeProject(p)] {
			return false
		}
	}
	return true
}

func saveCoverage(dir, host string, c Coverage) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := CoveragePath(dir, host)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// CorrectionClusters ranks the user-correction clusters of the stored sessions f
// selects, from the signals at or after f.Since and those with no known time,
// which is the set a scan's event filter keeps.
func CorrectionClusters(units []mine.SessionUnit, f transcript.Filter) []*report.Cluster {
	since := float64(f.Since.Unix())
	var signals []report.StoredSignal
	for _, u := range units {
		if !f.SelectsStored(u.Project, u.RepoRoot) {
			continue
		}
		for _, sig := range u.Signals {
			if sig.TS != 0 && sig.TS < since {
				continue
			}
			signals = append(signals, report.StoredSignal{SessionID: u.SessionID, Kind: sig.Kind, Text: sig.Text, TS: sig.TS})
		}
	}
	return report.CorrectionClusters(signals)
}
