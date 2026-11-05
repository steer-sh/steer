package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// scan streams one fixture and returns its events and counts.
func scan(t *testing.T, name string) ([]transcript.Event, transcript.Counts) {
	t.Helper()
	var events []transcript.Event
	counts, err := Adapter{}.Stream(
		transcript.Session{Harness: harness.Pi, Path: fixturePath(name)},
		transcript.Filter{},
		func(e transcript.Event) error {
			events = append(events, e)
			return nil
		})
	if err != nil {
		t.Fatalf("Stream(%s) returned %v", name, err)
	}
	return events, counts
}

// fixturePath resolves a fixture name under testdata, and passes an absolute
// path through: a fork fixture is rewritten into a temporary directory.
func fixturePath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join("testdata", name)
}

func kinds(events []transcript.Event) []transcript.Kind {
	out := make([]transcript.Kind, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func count(events []transcript.Event, kind transcript.Kind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestStreamNormalizesAnOrdinarySession(t *testing.T) {
	events, counts := scan(t, "normal.jsonl")

	want := []transcript.Kind{
		transcript.KindHumanTurn,
		transcript.KindToolCall,
		transcript.KindToolResult,
		transcript.KindToolCall,
		transcript.KindToolResult,
	}
	got := kinds(events)
	if len(got) != len(want) {
		t.Fatalf("emitted %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("emitted %v; want %v", got, want)
		}
	}
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d; a readable session skips nothing", counts.Skipped)
	}
	if counts.Bytes == 0 {
		t.Error("Bytes = 0; the stream reported no read")
	}

	for _, e := range events {
		if e.SessionID != "session-normal" {
			t.Fatalf("event carries session id %q; want the header's", e.SessionID)
		}
		if e.ThreadID != "" {
			t.Errorf("event carries thread id %q; Pi records no subagent thread", e.ThreadID)
		}
	}
	if events[0].Text != "Add sub(a,b) to calc.py" {
		t.Errorf("human turn text = %q", events[0].Text)
	}
	if events[1].Tool != "read" || events[1].Target != "/repo/demo/calc.py" || events[1].Shell {
		t.Errorf("read call = %+v", events[1])
	}
	if events[2].ToolUseID != "call-read-1" || events[2].IsError {
		t.Errorf("read result = %+v", events[2])
	}
	if !events[4].IsError {
		t.Error("the failing edit result did not report an error")
	}
}

// A thinking block and an assistant text block are not tool calls, and one
// assistant message can hold several calls. The count of emitted calls is what
// the friction classifier reads, so it must not include either.
func TestAssistantTextAndThinkingEmitNothing(t *testing.T) {
	events, _ := scan(t, "normal.jsonl")
	if got := count(events, transcript.KindToolCall); got != 2 {
		t.Errorf("emitted %d tool calls; want the 2 toolCall blocks", got)
	}
}

func TestUserContentIsAcceptedAsAStringOrAsBlocks(t *testing.T) {
	events, counts := scan(t, "bare-string-content.jsonl")
	if len(events) != 1 || events[0].Kind != transcript.KindHumanTurn || events[0].Text != "Hello" {
		t.Fatalf("bare-string content emitted %+v", events)
	}
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d; the string form is the format's own example", counts.Skipped)
	}
}

// Skipped names a line Steer could not read at all. A JSON parse failure and an
// entry naming no type are the whole set.
func TestOnlyAnUnreadableLineIsSkipped(t *testing.T) {
	events, counts := scan(t, "malformed.jsonl")
	if counts.Skipped != 2 {
		t.Errorf("Skipped = %d; want the unparsable line and the typeless entry", counts.Skipped)
	}
	if count(events, transcript.KindHumanTurn) != 1 {
		t.Errorf("a malformed line ended the stream: %v", kinds(events))
	}
}

// Pi defines entry types that carry no evidence, and ships new ones over time.
// Counting them as skipped would report every session as lossy.
func TestUnknownEntryTypesAreCountedNotSkipped(t *testing.T) {
	events, counts := scan(t, "unknown-entries.jsonl")
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d; an entry type Steer does not emit is not a coverage gap", counts.Skipped)
	}
	if counts.Entries != 6 {
		t.Errorf("Entries = %d; want every row of the file", counts.Entries)
	}
	if count(events, transcript.KindHumanTurn) != 1 {
		t.Errorf("emitted %v; want the one human turn", kinds(events))
	}
}

// A bashExecution row is a command the PERSON ran with ! or !!. Emitting it
// would attribute a person's own shell failure to the model.
func TestAUserRunCommandIsDroppedAndAModelCallStillEmits(t *testing.T) {
	events, counts := scan(t, "bash-execution.jsonl")
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d; a bashExecution row is read, not unreadable", counts.Skipped)
	}
	if got := kinds(events); len(got) != 2 ||
		got[0] != transcript.KindToolCall || got[1] != transcript.KindToolResult {
		t.Fatalf("emitted %v; want the model's bash call and its result alone", got)
	}
	if !events[0].Shell || events[0].Target != "go test ./..." {
		t.Errorf("model bash call = %+v; want the shell flag and the command", events[0])
	}
}

func TestCustomRolesAndCustomEntriesEmitNothing(t *testing.T) {
	events, counts := scan(t, "custom.jsonl")
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d", counts.Skipped)
	}
	if got := kinds(events); len(got) != 1 || got[0] != transcript.KindHumanTurn {
		t.Fatalf("emitted %v; want the one human turn", got)
	}
}

// The file is a tree. An /undo, a /tree switch or a retried prompt leaves the
// abandoned branch in the file, and a linear pass would emit its human turns
// into the same timeline — which is the count the session engine treats as its
// statistical unit.
func TestAnAbandonedBranchEmitsNothing(t *testing.T) {
	events, counts := scan(t, "branched.jsonl")
	if got := count(events, transcript.KindHumanTurn); got != 2 {
		t.Errorf("emitted %d human turns; want the 2 on the active branch", got)
	}
	for _, e := range events {
		if e.Text == "abandoned turn" || e.Target == "rm -rf /" || e.Text == "abandoned output" {
			t.Fatalf("an abandoned entry reached the stream: %+v", e)
		}
	}
	if got := count(events, transcript.KindToolCall); got != 2 {
		t.Errorf("emitted %d tool calls; want the 2 on the active branch", got)
	}
	// A search takes both a path and a pattern, and the pattern is the half
	// that names what was looked for.
	for _, e := range events {
		if e.Kind == transcript.KindToolCall && e.Tool == "grep" && e.Target != "TODO" {
			t.Errorf("grep call target = %q; want the pattern", e.Target)
		}
	}
	if counts.Entries != 11 {
		t.Errorf("Entries = %d; an abandoned row is still a row that was read", counts.Entries)
	}
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d", counts.Skipped)
	}
}

// A /fork over a chosen entry copies the parent's whole branch into the new
// file, keeping every copied entry's id. Both files then hold those turns, and
// counting the child on its own would double every one of them — the failure
// Codex's adapter avoids with an ordinal. Pi carries no ordinal, so the parent
// named in the header is what separates the copy from the fork's own work.
func TestAForkDoesNotRecountItsParentsEntries(t *testing.T) {
	parent, _ := scan(t, "fork-parent.jsonl")
	if count(parent, transcript.KindHumanTurn) != 1 {
		t.Fatalf("parent emitted %v", kinds(parent))
	}

	child, counts := scan(t, forkChild(t, filepath.Join("testdata", "fork-parent.jsonl")))
	if got := count(child, transcript.KindHumanTurn); got != 1 {
		t.Fatalf("the fork emitted %d human turns; want its own alone", got)
	}
	if child[0].Text != "child turn" {
		t.Errorf("the fork replayed its parent's turn: %+v", child[0])
	}
	if counts.Entries != 7 {
		t.Errorf("Entries = %d; a replayed row is still a row that was read", counts.Entries)
	}
	if parent[0].SessionID == child[0].SessionID {
		t.Error("the fork reused its parent's session id")
	}

	// With the parent gone, its entries are in no other session's count, so
	// counting them here is right rather than double.
	orphan, _ := scan(t, forkChild(t, filepath.Join(t.TempDir(), "deleted.jsonl")))
	if got := count(orphan, transcript.KindHumanTurn); got != 2 {
		t.Errorf("a fork whose parent is gone emitted %d turns; want both", got)
	}
}

// forkChild writes the fork fixture with its parentSession pointing at parent,
// and returns the path relative to testdata that scan takes. The fixture ships
// with a placeholder, because the header names an absolute path.
func forkChild(t *testing.T, parent string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "fork-child.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(parent)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "fork-child.jsonl")
	if err := os.WriteFile(name, []byte(strings.ReplaceAll(
		string(body), "FORK_PARENT_PATH", abs)), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// A file recording no link at all records no tree. Reading its last entry as
// the leaf would empty the session down to that one row.
func TestAnUnlinkableFileReadsAsWhollyActive(t *testing.T) {
	events, counts := scan(t, "unlinkable.jsonl")
	if got := count(events, transcript.KindHumanTurn); got != 3 {
		t.Errorf("emitted %d human turns; want every row of an unlinkable file", got)
	}
	if counts.Skipped != 0 {
		t.Errorf("Skipped = %d", counts.Skipped)
	}
}

// A line Steer cannot read is not a leaf. Reading one as the leaf would empty
// the file: nothing links to it, so the walk would stop on the row itself.
func TestATypelessTailIsNotReadAsTheLeaf(t *testing.T) {
	events, counts := scan(t, "typeless-tail.jsonl")
	if got := count(events, transcript.KindHumanTurn); got != 1 {
		t.Errorf("emitted %d human turns; want the one the file records", got)
	}
	if counts.Skipped != 1 {
		t.Errorf("Skipped = %d; want the typeless entry", counts.Skipped)
	}
}

func TestAHeaderOnlySessionEmitsNothing(t *testing.T) {
	events, counts := scan(t, "header-only.jsonl")
	if len(events) != 0 {
		t.Fatalf("an ephemeral session emitted %v", kinds(events))
	}
	if counts.Skipped != 0 || counts.Entries != 1 {
		t.Errorf("counts = %+v; want the header read and nothing skipped", counts)
	}
}

// Discover reads the project from each header's cwd. Pi mungs only '/' to '-',
// while NormalizeProject replaces every non-alphanumeric byte, so the directory
// name and the normalized project key never compare equal.
func TestDiscoverReadsTheProjectFromTheHeader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "--repo-demo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := Adapter{}.Discover(root, transcript.Filter{AllProjects: true})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Discover = %v, %v; want one session", sessions, err)
	}
	if sessions[0].Project != "/repo/demo" {
		t.Errorf("Project = %q; want the header cwd", sessions[0].Project)
	}
	if sessions[0].Harness != harness.Pi {
		t.Errorf("Harness = %q", sessions[0].Harness)
	}
	// The header cwd is the launch directory, which a repository scope resolves.
	if sessions[0].Cwd != "/repo/demo" {
		t.Errorf("Cwd = %q; want the header cwd", sessions[0].Cwd)
	}

	scoped, err := Adapter{}.Discover(root, transcript.Filter{Projects: []string{"/repo/demo"}})
	if err != nil || len(scoped) != 1 {
		t.Errorf("a scoped Discover found %v, %v; the header cwd did not match", scoped, err)
	}
	none, err := Adapter{}.Discover(root, transcript.Filter{Projects: []string{"/repo/other"}})
	if err != nil || len(none) != 0 {
		t.Errorf("a scoped Discover matched the wrong project: %v, %v", none, err)
	}
}

func TestRootPrefersTheSessionDirOverride(t *testing.T) {
	t.Setenv(SessionDirEnv, "/elsewhere/sessions")
	got, err := Adapter{}.Root()
	if err != nil || got != "/elsewhere/sessions" {
		t.Fatalf("Root = %q, %v; want the override", got, err)
	}

	t.Setenv(SessionDirEnv, "")
	t.Setenv("PI_CODING_AGENT_DIR", "/elsewhere/pi")
	got, err = Adapter{}.Root()
	if err != nil || got != filepath.Join("/elsewhere/pi", "sessions") {
		t.Errorf("Root = %q, %v; want sessions under the config directory", got, err)
	}
}

func TestDiscoverOnAMissingRootIsEmpty(t *testing.T) {
	sessions, err := Adapter{}.Discover(filepath.Join(t.TempDir(), "absent"), transcript.Filter{AllProjects: true})
	if err != nil || len(sessions) != 0 {
		t.Errorf("Discover over a missing root = %v, %v", sessions, err)
	}
}

var _ transcript.Adapter = Adapter{}

func TestContextTokensSumsTheCacheTermsOfTheActiveLeaf(t *testing.T) {
	// The abandoned assistant message is later in file order, so a linear read
	// would stamp its 99999 onto the row.
	got, ok := ContextTokens(fixturePath("usage-branch.jsonl"))
	if !ok || got != 2409 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensOfASessionWithNoUsage(t *testing.T) {
	if got, ok := ContextTokens(fixturePath("normal.jsonl")); ok || got != 0 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensOfAMissingSession(t *testing.T) {
	if _, ok := ContextTokens(filepath.Join(t.TempDir(), "gone.jsonl")); ok {
		t.Fatal("a missing session reported a depth")
	}
}

func TestContextTokensOfAnUnlinkableSessionReadsEveryEntry(t *testing.T) {
	// A file recording no tree reads as all-active, the same rule Stream follows.
	body, err := os.ReadFile(fixturePath("unlinkable.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte(`{"type": "message", "id": "e0000004", "parentId": null, "timestamp": "2026-04-19T03:17:27.000Z", "message": {"role": "assistant", "content": [], "usage": {"input": 5, "cacheRead": 20, "cacheWrite": 1}}}`+"\n")...)
	path := filepath.Join(t.TempDir(), "unlinkable.jsonl")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := ContextTokens(path); !ok || got != 26 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestEventsCarryTheHeadersWorkingDirectory(t *testing.T) {
	events, _ := scan(t, "normal.jsonl")
	if len(events) == 0 {
		t.Fatal("the fixture emitted no events")
	}
	for i, e := range events {
		if e.Cwd != "/repo/demo" {
			t.Errorf("event %d Cwd = %q; want %q", i, e.Cwd, "/repo/demo")
		}
	}
}
