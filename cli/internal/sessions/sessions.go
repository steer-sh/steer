// Package sessions is the transcript-friction corpus's store: it normalizes both
// harnesses' transcripts into one snapshot per root session and keeps them after the
// harness has rotated the transcript away. A frozen baseline has to outlive the
// evidence it was frozen over, and a transcript is the harness's file, not Steer's.
package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// stateSubdir is the corpus's directory under the state root.
const stateSubdir = "transcript-sessions"

// shardPrefix names the snapshot shards. A shard is this machine's; a synced directory
// holding another machine's shard is read and never rewritten.
const shardPrefix = "sessions"

// StateDir is where the snapshot shards live.
func StateDir() string { return statestore.Resolve(stateSubdir) }

// ShardPath is the file this machine writes.
func ShardPath(dir, host string) string {
	return filepath.Join(dir, shardPrefix+"."+host+".jsonl")
}

// Options are the inputs to a build.
type Options struct {
	// WindowDays bounds the transcript look-back. A session older than this is not
	// re-read; the snapshot already stored for it stands.
	WindowDays int
	// AllProjects scans every project rather than the working directory's.
	AllProjects bool
	// Only restricts the scan to one harness. Unknown scans every harness.
	Only harness.ID
}

// Build reads both harnesses' transcripts and returns one snapshot per root session,
// sorted. A harness whose transcript root is unreadable contributes nothing and never
// fails the build.
func Build(opts Options) ([]mine.SessionUnit, error) {
	projectPaths, err := report.ProjectPaths()
	if err != nil {
		return nil, err
	}
	since := time.Now().AddDate(0, 0, -opts.WindowDays)
	filter := transcript.Filter{Since: since, Projects: projectPaths, AllProjects: opts.AllProjects}
	instrument := report.ParserInstrument()

	byID := map[string]*mine.SessionUnit{}
	for _, adapter := range report.Adapters(opts.Only) {
		root, err := adapter.Root()
		if err != nil {
			continue
		}
		discovered, err := adapter.Discover(root, filter)
		if err != nil {
			continue
		}
		for _, session := range discovered {
			collectSession(adapter, session, since, instrument, byID)
		}
	}
	return sorted(byID), nil
}

// collectSession streams one transcript into the snapshot for its root session. A
// subagent thread is provenance, so its signals land on the root's snapshot.
func collectSession(a transcript.Adapter, s transcript.Session, since time.Time,
	instrument string, byID map[string]*mine.SessionUnit) {
	c := report.NewClassifier()
	project := transcript.NormalizeProject(s.Project)
	harnessID := string(a.Harness())

	_, _ = a.Stream(s, transcript.Filter{Since: since}, func(e transcript.Event) error {
		if e.SessionID == "" {
			return nil
		}
		key := harnessID + "\x1f" + e.SessionID
		u, ok := byID[key]
		if !ok {
			u = &mine.SessionUnit{
				Harness:    harnessID,
				SessionID:  e.SessionID,
				Project:    project,
				Instrument: instrument,
			}
			byID[key] = u
		}
		ts := parseTS(e.Timestamp)
		if ts > 0 {
			if u.StartTS == 0 || ts < u.StartTS {
				u.StartTS = ts
			}
			if ts > u.EndTS {
				u.EndTS = ts
			}
		}
		if sig, ok := c.Classify(e); ok {
			u.Signals = append(u.Signals, mine.SessionSignal{Kind: sig.Kind, Text: sig.Key, TS: ts})
		}
		return nil
	})
}

// Load reads every snapshot shard in the directory, newest write per session wins.
func Load(dir string) ([]mine.SessionUnit, error) {
	matches, err := filepath.Glob(filepath.Join(dir, shardPrefix+"*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return loadFiles(matches)
}

// LoadShard reads one host's shard alone. A refresh merges into this, never into the
// whole directory: a shard is one machine's, so folding another machine's sessions
// into ours would copy its rows under our name.
func LoadShard(dir, host string) ([]mine.SessionUnit, error) {
	return loadFiles([]string{ShardPath(dir, host)})
}

func loadFiles(paths []string) ([]mine.SessionUnit, error) {
	byID := map[string]*mine.SessionUnit{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var u mine.SessionUnit
			if err := json.Unmarshal([]byte(line), &u); err != nil {
				continue
			}
			if u.SessionID == "" {
				continue
			}
			copied := u
			byID[u.Harness+"\x1f"+u.SessionID] = &copied
		}
	}
	return sorted(byID), nil
}

// Merge folds a fresh build into the stored snapshots. A freshly built session replaces
// the stored one, because the transcript is the source of truth while it survives; a
// stored session the build did not reach is kept, which is the whole point of the store.
func Merge(stored, fresh []mine.SessionUnit) []mine.SessionUnit {
	byID := map[string]*mine.SessionUnit{}
	for _, list := range [][]mine.SessionUnit{stored, fresh} {
		for i := range list {
			u := list[i]
			byID[u.Harness+"\x1f"+u.SessionID] = &u
		}
	}
	return sorted(byID)
}

// Save writes the snapshots to this machine's shard, atomically. It sorts what it is
// given rather than trusting the caller's order, and each row's keys are ordered by the
// struct, so a second save over identical evidence is byte-identical.
func Save(dir, host string, units []mine.SessionUnit) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, u := range sortUnits(units) {
		raw, err := json.Marshal(u)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	path := ShardPath(dir, host)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Refresh builds, merges and saves in one pass, returning the whole stored corpus.
// The merge is against this host's own shard: the build reaches this machine's
// transcripts, so only this machine's shard may grow from it. Another machine's shard
// is read into the returned corpus and never rewritten.
func Refresh(dir, host string, opts Options) ([]mine.SessionUnit, error) {
	own, err := LoadShard(dir, host)
	if err != nil {
		return nil, err
	}
	fresh, err := Build(opts)
	if err != nil {
		stored, _ := Load(dir)
		return stored, err
	}
	if err := Save(dir, host, Merge(own, fresh)); err != nil {
		return nil, err
	}
	return Load(dir)
}

// sorted renders the map in one deterministic order.
func sorted(byID map[string]*mine.SessionUnit) []mine.SessionUnit {
	out := make([]mine.SessionUnit, 0, len(byID))
	for _, u := range byID {
		out = append(out, *u)
	}
	return sortUnits(out)
}

// sortUnits is the store's one order: harness, then session id, with each session's
// signals ordered by time, kind and text. It copies, so the caller's slice keeps its
// own order.
func sortUnits(units []mine.SessionUnit) []mine.SessionUnit {
	out := make([]mine.SessionUnit, len(units))
	copy(out, units)
	for i := range out {
		signals := make([]mine.SessionSignal, len(out[i].Signals))
		copy(signals, out[i].Signals)
		sort.SliceStable(signals, func(a, b int) bool {
			x, y := signals[a], signals[b]
			if x.TS != y.TS {
				return x.TS < y.TS
			}
			if x.Kind != y.Kind {
				return x.Kind < y.Kind
			}
			return x.Text < y.Text
		})
		out[i].Signals = signals
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Harness != out[j].Harness {
			return out[i].Harness < out[j].Harness
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// parseTS reads a harness's own timestamp string into unix seconds. An unparsable or
// absent stamp reads as 0, which leaves the session's window unbounded on that side.
func parseTS(s string) float64 {
	if s == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.Unix())
		}
	}
	return 0
}
