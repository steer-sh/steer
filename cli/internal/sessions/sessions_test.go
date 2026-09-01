package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
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
		t.Errorf("a second save drifted.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if len(loaded) != 2 || loaded[0].Harness != "claude-code" {
		t.Errorf("the store sorts by harness then session, got %+v", loaded)
	}
}

// The store outlives the transcript: a session the rebuild no longer reaches is kept,
// and a session the rebuild did reach is replaced by the fresh read.
func TestMergeKeepsWhatTheRebuildNoLongerReaches(t *testing.T) {
	stored := []mine.SessionUnit{
		unit("claude-code", "rotated", 1000, 1100, "bare python"),
		unit("claude-code", "live", 2000, 2100, "stale text"),
	}
	fresh := []mine.SessionUnit{
		unit("claude-code", "live", 2000, 2200, "fresh text"),
		unit("codex", "new", 3000, 3100, "bare python"),
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
	live := byID["claude-code/live"]
	if live.Signals[0].Text != "fresh text" || live.EndTS != 2200 {
		t.Errorf("the fresh read must win over the stored snapshot, got %+v", live)
	}
	if _, ok := byID["codex/new"]; !ok {
		t.Error("a newly built session must land in the store")
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
	t.Setenv("HOME", t.TempDir())
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
