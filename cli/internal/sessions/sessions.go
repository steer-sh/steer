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
	// WindowDays bounds the transcript look-back. A transcript file last modified
	// before it is not re-read, and what it contributed before stands. A file
	// inside the window is read whole.
	WindowDays int
	// Scope selects which sessions the scan reads. The zero value reads every
	// project, which is what a cross-project mining read wants.
	Scope report.Scope
	// Only restricts the scan to one harness. Unknown scans every harness.
	Only harness.ID
}

// Fresh is one build: a snapshot per root session it read, and every transcript file
// it read to the end without error, including a file that yielded no session.
type Fresh struct {
	Units []mine.SessionUnit
	Read  map[string]bool
}

// Build reads both harnesses' transcripts and returns one snapshot per root session,
// sorted. A harness whose transcript root is unreadable contributes nothing and never
// fails the build.
func Build(opts Options) (Fresh, error) {
	since := time.Now().AddDate(0, 0, -opts.WindowDays)
	// One resolver serves both the scope predicate and each session's stored
	// root, so a directory is resolved once however the scan selects.
	resolve := report.RootResolver()
	filter, err := opts.Scope.FilterWith(since, resolve)
	if err != nil {
		return Fresh{}, err
	}
	parser := report.ParserInstrument()

	byID := map[string]*mine.SessionUnit{}
	// seeded marks the sessions whose launch directory has already been read. A
	// session's entries can span several files, so it outlives one collectSession.
	seeded := map[string]bool{}
	read := map[string]bool{}
	for _, adapter := range report.Adapters(opts.Only) {
		root, err := adapter.Root()
		if err != nil {
			continue
		}
		discovered, err := adapter.Discover(root, filter)
		if err != nil {
			continue
		}
		// The adapter decides the fields the classifier reads, so a unit's
		// instrument names both halves of the pipeline that produced it.
		instrument := mine.ComposeSessionInstrument(parser, adapter.Instrument())
		for _, session := range discovered {
			if collectSession(adapter, session, instrument, resolve, seeded, byID) {
				read[sourcePath(session.Path)] = true
			}
		}
	}
	for _, u := range byID {
		derive(u)
	}
	return Fresh{Units: sorted(byID), Read: read}, nil
}

// collectSession streams one whole transcript file into the snapshots of the root
// sessions it names, one source contribution each, and reports whether the read was
// clean. The window selected the file and never trims its events: a resumed
// session's early entries are as much its own as its late ones. A failed read adds
// nothing. A subagent thread is provenance, so its signals land on the root's
// snapshot.
func collectSession(a transcript.Adapter, s transcript.Session, instrument string,
	resolve func(string) string, seeded map[string]bool,
	byID map[string]*mine.SessionUnit) bool {
	c := report.NewClassifier()
	path := sourcePath(s.Path)
	staged := map[string]*mine.SessionSource{}

	_, err := a.Stream(s, transcript.Filter{}, func(e transcript.Event) error {
		if e.SessionID == "" {
			return nil
		}
		src, ok := staged[e.SessionID]
		if !ok {
			src = &mine.SessionSource{Path: path, Instrument: instrument}
			staged[e.SessionID] = src
		}
		ts := parseTS(e.Timestamp)
		if ts > 0 {
			if src.StartTS == 0 || ts < src.StartTS {
				src.StartTS = ts
			}
			if ts > src.EndTS {
				src.EndTS = ts
			}
		}
		if sig, ok := c.Classify(e); ok {
			src.Signals = append(src.Signals, mine.SessionSignal{Kind: sig.Kind, Text: sig.Key, TS: ts})
		}
		return nil
	})
	if err != nil {
		return false
	}

	project := transcript.NormalizeProject(s.Project)
	harnessID := string(a.Harness())
	// The adapter read the launch directory at discovery, so a session resolves to
	// where it started rather than to a directory an event recorded.
	root := resolve(s.Cwd)
	for id, src := range staged {
		key := harnessID + "\x1f" + id
		u, ok := byID[key]
		if !ok {
			u = &mine.SessionUnit{
				Harness:    harnessID,
				SessionID:  id,
				Project:    project,
				Instrument: instrument,
			}
			byID[key] = u
		}
		// The launch directory decides the root even when it resolves to none: a
		// session that started outside every repository must stay unresolved
		// rather than adopt the first repository it visits.
		if s.Cwd != "" && !seeded[key] {
			seeded[key] = true
			u.RepoRoot = root
		}
		u.Sources = append(u.Sources, *src)
	}
	return true
}

// sourcePath is a transcript file's identity as a source contribution.
func sourcePath(path string) string { return filepath.Clean(path) }

// derive recomputes a session's bounds and signals from its sources. A signal that a
// legacy contribution shares with a file-backed source is shown once, because the
// legacy snapshot was read from one of those files.
func derive(u *mine.SessionUnit) {
	u.StartTS, u.EndTS, u.Signals = 0, 0, nil
	backed := map[mine.SessionSignal]int{}
	for _, src := range u.Sources {
		if src.Path != "" {
			for _, sig := range src.Signals {
				backed[sig]++
			}
		}
	}
	for _, src := range u.Sources {
		if src.StartTS > 0 && (u.StartTS == 0 || src.StartTS < u.StartTS) {
			u.StartTS = src.StartTS
		}
		if src.EndTS > u.EndTS {
			u.EndTS = src.EndTS
		}
		for _, sig := range src.Signals {
			if src.Path == "" && backed[sig] > 0 {
				backed[sig]--
				continue
			}
			u.Signals = append(u.Signals, sig)
		}
	}
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

// Merge folds a fresh build into the stored snapshots one source file at a time. A
// file the build read replaces everything it contributed before, so a reread can
// retract a signal or a whole session; a contribution from a file the build did not
// read is kept, which is the whole point of the store. A session stored before
// sources were recorded keeps its whole snapshot as one legacy contribution beside
// the fresh ones, because its files are unknown.
func Merge(stored []mine.SessionUnit, fresh Fresh) []mine.SessionUnit {
	byID := map[string]*mine.SessionUnit{}
	changed := map[string]bool{}
	legacy := map[string]bool{}
	for i := range stored {
		u := stored[i]
		key := u.Harness + "\x1f" + u.SessionID
		legacy[key] = len(u.Sources) == 0
		var kept []mine.SessionSource
		for _, src := range u.Sources {
			if src.Path == "" || !fresh.Read[src.Path] {
				kept = append(kept, src)
			}
		}
		if len(kept) != len(u.Sources) {
			changed[key] = true
		}
		u.Sources = kept
		byID[key] = &u
	}
	for i := range fresh.Units {
		u := fresh.Units[i]
		key := u.Harness + "\x1f" + u.SessionID
		changed[key] = true
		prev, ok := byID[key]
		if !ok {
			byID[key] = &u
			continue
		}
		if legacy[key] {
			prev.Sources = []mine.SessionSource{legacySource(*prev)}
		}
		prev.Sources = append(prev.Sources, u.Sources...)
		prev.Project, prev.Instrument = u.Project, u.Instrument
		// A root resolved once is kept when a later build cannot resolve it,
		// because the directory a rebuild reads may no longer exist.
		if u.RepoRoot != "" {
			prev.RepoRoot = u.RepoRoot
		}
	}
	for key := range changed {
		u := byID[key]
		if len(u.Sources) == 0 {
			delete(byID, key)
			continue
		}
		derive(u)
	}
	return sorted(byID)
}

// legacySource is a snapshot stored before sources were recorded, kept whole.
func legacySource(u mine.SessionUnit) mine.SessionSource {
	return mine.SessionSource{StartTS: u.StartTS, EndTS: u.EndTS, Signals: u.Signals, Instrument: u.Instrument}
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
// sources ordered by path and every signal list by time, kind and text. It copies, so
// the caller's slice keeps its own order.
func sortUnits(units []mine.SessionUnit) []mine.SessionUnit {
	out := make([]mine.SessionUnit, len(units))
	copy(out, units)
	for i := range out {
		out[i].Signals = sortSignals(out[i].Signals)
		if len(out[i].Sources) == 0 {
			continue
		}
		sources := make([]mine.SessionSource, len(out[i].Sources))
		copy(sources, out[i].Sources)
		for j := range sources {
			sources[j].Signals = sortSignals(sources[j].Signals)
		}
		sort.SliceStable(sources, func(a, b int) bool { return sources[a].Path < sources[b].Path })
		out[i].Sources = sources
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Harness != out[j].Harness {
			return out[i].Harness < out[j].Harness
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

func sortSignals(in []mine.SessionSignal) []mine.SessionSignal {
	signals := make([]mine.SessionSignal, len(in))
	copy(signals, in)
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
	return signals
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
