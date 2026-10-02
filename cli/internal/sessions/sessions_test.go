package sessions

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/testhome"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func unit(harness, id string, start, end float64, texts ...string) mine.SessionUnit {
	u := mine.SessionUnit{
		Harness: harness, SessionID: id, Project: "proj",
		StartTS: start, EndTS: end, Instrument: "parser/1",
	}
	for i, text := range texts {
		u.Signals = append(u.Signals, mine.SessionSignal{
			Kind: mine.SignalCorrection, Text: text, TS: start + float64(i),
		})
	}
	return u
}

// source is one file-backed contribution of signals at the given times.
func source(path string, start, end float64, texts ...string) mine.SessionSource {
	src := mine.SessionSource{Path: path, StartTS: start, EndTS: end, Instrument: "parser/1"}
	for i, text := range texts {
		src.Signals = append(src.Signals, mine.SessionSignal{
			Kind: mine.SignalCorrection, Text: text, TS: start + float64(i),
		})
	}
	return src
}

// sourced is a session built from its sources, as the store derives one.
func sourced(harness, id string, sources ...mine.SessionSource) mine.SessionUnit {
	u := mine.SessionUnit{Harness: harness, SessionID: id, Project: "proj", Instrument: "parser/1", Sources: sources}
	derive(&u)
	return u
}

func texts(u mine.SessionUnit) []string {
	var out []string
	for _, sig := range u.Signals {
		out = append(out, sig.Text)
	}
	return out
}

// A second save over identical evidence must be byte-identical, or the store's own
// mtime becomes a change signal no reader can trust.
func TestSaveIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	units := []mine.SessionUnit{
		unit("codex", "s2", 2000, 2100, "bare python", "another"),
		unit("claude-code", "s1", 1000, 1100, "bare python"),
	}
	if err := Save(dir, "h", units); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(ShardPath(dir, "h"))
	if err != nil {
		t.Fatal(err)
	}
	// Reload and save the round-tripped value: the order must come from the store,
	// not from the order the caller happened to hold.
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, "h", loaded); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(ShardPath(dir, "h"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a round-tripped save changed bytes:\n%s\n---\n%s", first, second)
	}

	// Sources the caller holds in another order save to the same bytes.
	a, b := source("/t/a.jsonl", 1000, 1001, "x"), source("/t/b.jsonl", 1002, 1003, "y")
	if err := Save(dir, "h", []mine.SessionUnit{sourced("codex", "s3", b, a)}); err != nil {
		t.Fatal(err)
	}
	first, _ = os.ReadFile(ShardPath(dir, "h"))
	if err := Save(dir, "h", []mine.SessionUnit{sourced("codex", "s3", a, b)}); err != nil {
		t.Fatal(err)
	}
	second, _ = os.ReadFile(ShardPath(dir, "h"))
	if string(first) != string(second) {
		t.Errorf("a second save drifted.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if len(loaded) != 2 || loaded[0].Harness != "claude-code" {
		t.Errorf("the store sorts by harness then session, got %+v", loaded)
	}
}

// The store outlives the transcript: a session the rebuild no longer reaches is kept,
// and a file the rebuild did read replaces what it contributed, so a reread under a
// new instrument can retract a signal.
func TestMergeKeepsWhatTheRebuildNoLongerReaches(t *testing.T) {
	stored := []mine.SessionUnit{
		sourced("claude-code", "rotated", source("/t/rotated.jsonl", 1000, 1100, "bare python")),
		sourced("claude-code", "live", source("/t/live.jsonl", 2000, 2100, "stale text")),
	}
	live := sourced("claude-code", "live", source("/t/live.jsonl", 2000, 2200, "fresh text"))
	live.Instrument, live.Sources[0].Instrument = "parser/2", "parser/2"
	fresh := Fresh{
		Units: []mine.SessionUnit{live, sourced("codex", "new", source("/t/new.jsonl", 3000, 3100, "bare python"))},
		Read:  map[string]bool{"/t/live.jsonl": true, "/t/new.jsonl": true},
	}
	merged := Merge(stored, fresh)
	byID := map[string]mine.SessionUnit{}
	for _, u := range merged {
		byID[u.Harness+"/"+u.SessionID] = u
	}
	if len(merged) != 3 {
		t.Fatalf("want 3 sessions, got %d: %+v", len(merged), merged)
	}
	if _, ok := byID["claude-code/rotated"]; !ok {
		t.Error("a rotated-away session must survive in the store")
	}
	got := byID["claude-code/live"]
	if strings.Join(texts(got), ",") != "fresh text" || got.EndTS != 2200 || got.Instrument != "parser/2" {
		t.Errorf("the reread file must replace its own contribution, got %+v", got)
	}
	if _, ok := byID["codex/new"]; !ok {
		t.Error("a newly built session must land in the store")
	}
}

// A file the rebuild did not reach keeps its contribution beside one it reread, in
// either order: a resumed parent must not erase an older child, nor the reverse.
func TestMergeKeepsTheSiblingFileTheRebuildDidNotRead(t *testing.T) {
	for _, c := range []struct {
		reread, want string
		start        float64
	}{
		{"/t/parent.jsonl", "child correction,resumed", 2000},
		{"/t/child.jsonl", "parent correction,resumed", 1000},
	} {
		reread := c.reread
		stored := []mine.SessionUnit{sourced("codex", "root",
			source("/t/parent.jsonl", 1000, 1100, "parent correction"),
			source("/t/child.jsonl", 2000, 2100, "child correction"))}
		fresh := Fresh{
			Units: []mine.SessionUnit{sourced("codex", "root", source(reread, 5000, 5100, "resumed"))},
			Read:  map[string]bool{reread: true},
		}
		merged := Merge(stored, fresh)
		if len(merged) != 1 {
			t.Fatalf("want one session, got %+v", merged)
		}
		got := merged[0]
		if strings.Join(texts(got), ",") != c.want || got.StartTS != c.start {
			t.Errorf("rereading %s: got signals %v start %v, want %s from %v",
				reread, texts(got), got.StartTS, c.want, c.start)
		}
	}
}

// A clean reread that no longer names a session retracts that file's contribution,
// and a session left with no source leaves the store.
func TestMergeRetractsWhatACleanRereadNoLongerEmits(t *testing.T) {
	stored := []mine.SessionUnit{
		sourced("pi", "two", source("/t/a.jsonl", 1000, 1100, "abandoned branch"), source("/t/b.jsonl", 2000, 2100, "kept")),
		sourced("pi", "one", source("/t/c.jsonl", 3000, 3100, "gone")),
	}
	merged := Merge(stored, Fresh{Read: map[string]bool{"/t/a.jsonl": true, "/t/c.jsonl": true}})
	if len(merged) != 1 || merged[0].SessionID != "two" {
		t.Fatalf("a session with no remaining source must leave the store, got %+v", merged)
	}
	if got := texts(merged[0]); strings.Join(got, ",") != "kept" || merged[0].StartTS != 2000 {
		t.Errorf("the reread file's signals must be retracted, got %v start %v", got, merged[0].StartTS)
	}
}

// A session stored before sources were recorded keeps its whole snapshot: a signal
// later than the fresh start and an end later than the fresh end may belong to a
// file this build did not read. A signal the fresh source also carries shows once,
// and a second refresh changes nothing.
func TestMergeKeepsALegacySnapshotWhole(t *testing.T) {
	stored := []mine.SessionUnit{unit("codex", "old", 1000, 5000, "shared", "unmatched")}
	stored[0].Signals[1].TS = 4000
	fresh := Fresh{
		Units: []mine.SessionUnit{sourced("codex", "old", source("/t/parent.jsonl", 1000, 3000, "shared"))},
		Read:  map[string]bool{"/t/parent.jsonl": true},
	}
	once := Merge(stored, fresh)
	twice := Merge(once, fresh)
	for i, merged := range [][]mine.SessionUnit{once, twice} {
		if len(merged) != 1 {
			t.Fatalf("merge %d: want one session, got %+v", i+1, merged)
		}
		got := merged[0]
		if strings.Join(texts(got), ",") != "shared,unmatched" {
			t.Errorf("merge %d: signals %v", i+1, texts(got))
		}
		if got.StartTS != 1000 || got.EndTS != 5000 {
			t.Errorf("merge %d: bounds %v-%v, want the legacy end kept", i+1, got.StartTS, got.EndTS)
		}
	}
}

// A refresh reads a resumed transcript whole: its early entries are older than the
// window, but they are the session's own, and the session spans an intervention
// made between them.
func TestRefreshKeepsAResumedSessionsEarlyEntries(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	project := filepath.Join(home, ".claude", "projects", "-t-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	row := func(ts time.Time, text string) string {
		return fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"message":{"content":%q}}`+"\n",
			ts.UTC().Format(time.RFC3339), text)
	}
	early := time.Now().AddDate(0, 0, -45)
	body := row(early, "don't use var") + row(time.Now().Add(-time.Hour), "ok continue")
	if err := os.WriteFile(filepath.Join(project, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, days := range []int{60, 30} {
		if _, err := Refresh(dir, "h", Options{WindowDays: days}); err != nil {
			t.Fatal(err)
		}
	}
	units, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || len(units[0].Signals) != 1 || units[0].StartTS != float64(early.Unix()) {
		t.Fatalf("the early correction and start must survive, got %+v", units)
	}
	_, _, partial := mine.SplitSessions(units, float64(time.Now().AddDate(0, 0, -10).Unix()))
	if len(partial) != 1 {
		t.Errorf("a session resumed across an intervention spans it, got partial=%d", len(partial))
	}

	// The stored row names its file, so rereading the file after its correction is
	// gone retracts the correction rather than keeping it as unknown evidence.
	if err := os.WriteFile(filepath.Join(project, "s1.jsonl"), []byte(row(early, "ok start")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(dir, "h", Options{WindowDays: 30}); err != nil {
		t.Fatal(err)
	}
	if units, _ = Load(dir); len(units) != 1 || len(units[0].Signals) != 0 {
		t.Errorf("a reread file must retract its own signal, got %+v", units)
	}
}

// A shard from another machine is read and never rewritten, and an unreadable row
// costs only itself.
func TestLoadReadsEveryShardAndSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "hostA", []mine.SessionUnit{unit("claude-code", "a1", 1000, 1100)}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "sessions.hostB.jsonl")
	body := `{"harness":"codex","session_id":"b1","start_ts":2000}` + "\n" +
		"not json\n" + `{"harness":"codex","session_id":""}` + "\n"
	if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("want one session per shard, got %+v", loaded)
	}
	if err := Save(dir, "hostA", loaded); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Error("another machine's shard must not be rewritten")
	}
}

// A refresh may grow only this machine's shard. Another machine's sessions are read
// into the returned corpus and never copied under our host's name.
func TestRefreshNeverRewritesAnotherHostsSessions(t *testing.T) {
	// HOME points at an empty sandbox, so the build reaches no transcript and the
	// merge is the stored shard alone.
	testhome.Set(t, t.TempDir())
	dir := t.TempDir()
	ours := []mine.SessionUnit{unit("claude-code", "mine", 1000, 1100, "bare python")}
	if err := Save(dir, "hostA", ours); err != nil {
		t.Fatal(err)
	}
	theirs := `{"harness":"codex","session_id":"theirs","start_ts":2000}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sessions.hostB.jsonl"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := Refresh(dir, "hostA", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("the returned corpus spans every shard, got %+v", all)
	}
	own, err := LoadShard(dir, "hostA")
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || own[0].SessionID != "mine" {
		t.Errorf("our shard must hold only our sessions, got %+v", own)
	}
}

func TestMergeKeepsARootTheRebuildCouldNotResolve(t *testing.T) {
	withRoot := func(u mine.SessionUnit, root string) mine.SessionUnit {
		u.RepoRoot = root
		return u
	}
	stored := []mine.SessionUnit{
		withRoot(unit("claude-code", "live", 2000, 2100), "/repo/demo"),
		withRoot(unit("claude-code", "moved", 2000, 2100), "/repo/demo"),
	}
	fresh := []mine.SessionUnit{
		unit("claude-code", "live", 2000, 2200),
		withRoot(unit("claude-code", "moved", 2000, 2200), "/repo/elsewhere"),
	}
	byID := map[string]mine.SessionUnit{}
	for _, u := range Merge(stored, Fresh{Units: fresh}) {
		byID[u.SessionID] = u
	}
	if got := byID["live"].RepoRoot; got != "/repo/demo" {
		t.Errorf("an unresolved rebuild dropped the stored root: got %q", got)
	}
	if got := byID["moved"].RepoRoot; got != "/repo/elsewhere" {
		t.Errorf("a resolved rebuild must win: got %q", got)
	}
}

// stubAdapter emits a fixed event list, so a test can decide what working
// directories a session recorded and in what order.
type stubAdapter struct {
	events []transcript.Event
	err    error
}

func (stubAdapter) Harness() harness.ID   { return harness.ClaudeCode }
func (stubAdapter) Instrument() string    { return "claude-code:test" }
func (stubAdapter) Root() (string, error) { return "", nil }
func (stubAdapter) Discover(string, transcript.Filter) ([]transcript.Session, error) {
	return nil, nil
}

func (a stubAdapter) Stream(_ transcript.Session, _ transcript.Filter,
	emit func(transcript.Event) error) (transcript.Counts, error) {
	for _, e := range a.events {
		if err := emit(e); err != nil {
			return transcript.Counts{}, err
		}
	}
	return transcript.Counts{}, a.err
}

// A read that fails after emitting events adds nothing and is not reported clean, so
// a partial file can neither add signals nor retract the ones stored for it.
func TestCollectSessionCommitsNothingFromAFailedRead(t *testing.T) {
	events := []transcript.Event{{
		Kind: transcript.KindHumanTurn, SessionID: "s1", Text: "don't use var",
		Timestamp: "2026-07-10T00:00:00Z",
	}}
	byID := map[string]*mine.SessionUnit{}
	ok := collectSession(stubAdapter{events: events, err: errors.New("truncated")},
		transcript.Session{Path: "/t/s1.jsonl"}, "parser/1", report.RootResolver(), map[string]bool{}, byID)
	if ok || len(byID) != 0 {
		t.Errorf("a failed read must commit nothing: clean=%v units=%+v", ok, byID)
	}
}

// A session's root is the launch directory the adapter read at discovery, not a
// directory an event recorded. A harness records the live shell directory, and
// the time window can hide the entries from before it, so neither can decide.
func TestTheLaunchDirectoryDecidesTheRootEvenWhenItResolvesToNone(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %s", out)
	}
	outside := t.TempDir()

	collect := func(launch string, cwds ...string) mine.SessionUnit {
		var events []transcript.Event
		for i, cwd := range cwds {
			events = append(events, transcript.Event{
				Kind: transcript.KindHumanTurn, SessionID: "s1",
				Timestamp: "2026-07-10T00:00:0" + string(rune('0'+i)) + "Z", Cwd: cwd,
			})
		}
		byID := map[string]*mine.SessionUnit{}
		collectSession(stubAdapter{events: events}, transcript.Session{Cwd: launch},
			"parser/1", report.RootResolver(), map[string]bool{}, byID)
		return *byID["claude-code\x1fs1"]
	}

	if got := collect(outside, repo, repo).RepoRoot; got != "" {
		t.Errorf("a session launched outside a repository took the root it visited: %q", got)
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(repo, outside).RepoRoot; got != resolved {
		t.Errorf("the launch directory must decide the root: got %q, want %q", got, resolved)
	}
	if got := collect("", repo).RepoRoot; got != "" {
		t.Errorf("a session with no launch directory must stay unresolved: %q", got)
	}
}
