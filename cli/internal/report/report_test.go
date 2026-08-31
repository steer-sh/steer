package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/cc"
)

func writeTranscript(t *testing.T, dir, name string, lines []string) transcript.Session {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return transcript.Session{Harness: harness.ClaudeCode, Path: path, Project: filepath.Base(dir)}
}

func userTextEntry(session, ts, text string) string {
	e := map[string]any{
		"type": "user", "sessionId": session, "timestamp": ts,
		"message": map[string]any{"role": "user", "content": text},
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func toolUseEntry(session, id, tool, command string) string {
	e := map[string]any{
		"type": "assistant", "sessionId": session, "timestamp": "2026-07-10T00:00:00Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": tool, "input": map[string]any{"command": command}},
		}},
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func toolResultEntry(session, id, text string, isError bool) string {
	e := map[string]any{
		"type": "user", "sessionId": session, "timestamp": "2026-07-10T00:00:01Z",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isError},
		}},
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func TestScannerExtractsCorrections(t *testing.T) {
	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s1.jsonl", []string{
		userTextEntry("s1", "2026-07-10T00:00:00Z", "don't use console.log, use the logger"),
		userTextEntry("s1", "2026-07-11T00:00:00Z", "I said don't use console.log please"),
		userTextEntry("s2", "2026-07-12T00:00:00Z", "looks good, ship it"),
	})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, []string{"p"}, nil)

	if len(d.Signals.UserCorrections) != 2 {
		t.Fatalf("expected 2 correction clusters, got %d: %+v", len(d.Signals.UserCorrections), d.Signals.UserCorrections)
	}
	if d.Stats.SessionsScanned != 2 {
		t.Errorf("expected 2 sessions, got %d", d.Stats.SessionsScanned)
	}
}

func TestScannerClustersRepeatedCorrections(t *testing.T) {
	dir := t.TempDir()
	lines := []string{}
	for i := 0; i < 4; i++ {
		lines = append(lines, userTextEntry(fmt.Sprintf("s%d", i), "2026-07-10T00:00:00Z", "stop using var in javascript"))
	}
	tf := writeTranscript(t, dir, "s.jsonl", lines)

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)

	if len(d.Signals.UserCorrections) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(d.Signals.UserCorrections))
	}
	c := d.Signals.UserCorrections[0]
	if c.Count != 4 || c.Sessions != 4 {
		t.Errorf("expected count=4 sessions=4, got count=%d sessions=%d", c.Count, c.Sessions)
	}
	if len(c.Examples) != maxExamplesPerCluster {
		t.Errorf("expected %d examples, got %d", maxExamplesPerCluster, len(c.Examples))
	}
}

func TestScannerIgnoresMetaSidechainAndCommands(t *testing.T) {
	dir := t.TempDir()
	meta := `{"type":"user","isMeta":true,"sessionId":"s1","timestamp":"2026-07-10T00:00:00Z","message":{"content":"don't use var"}}`
	sidechain := `{"type":"user","isSidechain":true,"sessionId":"s1","timestamp":"2026-07-10T00:00:00Z","message":{"content":"don't use var"}}`
	command := userTextEntry("s1", "2026-07-10T00:00:00Z", "<command-name>/steer</command-name>never use foo")
	tf := writeTranscript(t, dir, "s.jsonl", []string{meta, sidechain, command})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)
	if len(d.Signals.UserCorrections) != 0 {
		t.Fatalf("expected 0 clusters, got %+v", d.Signals.UserCorrections)
	}
}

func TestScannerWindowFiltersOldEntries(t *testing.T) {
	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s.jsonl", []string{
		userTextEntry("s1", "2026-01-01T00:00:00Z", "don't use var"),
		userTextEntry("s1", "2026-07-10T00:00:00Z", "don't use eval"),
	})

	since, _ := time.Parse(time.RFC3339, "2026-07-01T00:00:00Z")
	s := NewScanner(since)
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)
	if len(d.Signals.UserCorrections) != 1 {
		t.Fatalf("expected 1 cluster after window filter, got %d", len(d.Signals.UserCorrections))
	}
	if !strings.Contains(d.Signals.UserCorrections[0].Pattern, "eval") {
		t.Errorf("wrong cluster survived: %q", d.Signals.UserCorrections[0].Pattern)
	}
}

func TestScannerAttributesToolSignals(t *testing.T) {
	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s.jsonl", []string{
		toolUseEntry("s1", "t1", "Bash", "npm test"),
		toolResultEntry("s1", "t1", "Test Suites: 1 failed\nFAIL src/app.test.js", true),
		toolUseEntry("s1", "t2", "Bash", "rm -rf build"),
		toolResultEntry("s1", "t2", "The user doesn't want to proceed with this tool use.", true),
		toolUseEntry("s1", "t3", "Edit", ""),
		toolResultEntry("s1", "t3", "PreToolUse:Edit hook blocked this operation: no console.log", true),
	})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)

	if len(d.Signals.ToolFailures) != 1 {
		t.Errorf("expected 1 tool failure, got %+v", d.Signals.ToolFailures)
	}
	if len(d.Signals.UserRejections) != 1 {
		t.Errorf("expected 1 rejection, got %+v", d.Signals.UserRejections)
	}
	if len(d.Signals.HookBlocks) != 1 {
		t.Errorf("expected 1 hook block, got %+v", d.Signals.HookBlocks)
	}
}

func TestDigestEvictionRespectsBudget(t *testing.T) {
	d := NewDigest(30)
	for i := 0; i < 200; i++ {
		d.Signals.UserCorrections = append(d.Signals.UserCorrections, &Cluster{
			Pattern:  fmt.Sprintf("pattern number %d with some padding text to occupy bytes", i),
			Count:    i + 1,
			Sessions: 1,
			Examples: []string{strings.Repeat("x", 200)},
		})
	}

	budget := 8000
	data, err := d.Marshal(budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > budget {
		t.Fatalf("digest %d bytes exceeds budget %d", len(data), budget)
	}
	if !d.Stats.Truncated {
		t.Error("expected truncated=true")
	}
	if d.Stats.ClustersEmitted >= d.Stats.ClustersFound {
		t.Errorf("expected eviction: emitted=%d found=%d", d.Stats.ClustersEmitted, d.Stats.ClustersFound)
	}

	// Highest-scoring cluster must survive eviction.
	survived := d.Signals.UserCorrections
	if len(survived) == 0 {
		t.Fatal("all clusters evicted")
	}
	if survived[0].Count != 200 {
		t.Errorf("expected top cluster (count=200) to survive, got count=%d", survived[0].Count)
	}

	var parsed Digest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("digest is not valid JSON: %v", err)
	}
	if parsed.DigestVersion != DigestVersion {
		t.Errorf("digest_version = %d, want %d", parsed.DigestVersion, DigestVersion)
	}
}

func TestExtractInstructionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	content := `# Project

## Conventions
- Always use python3 not python
- Prefer tabs over spaces
  - nested elaboration should be ignored

## Steer Rules
<!-- managed-by-steer -->
- never use console.log | use logger instead

## Build
- Run make test before committing
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := ExtractInstructionFile(harness.ClaudeCode, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(md.SteerRules) != 1 || !strings.Contains(md.SteerRules[0], "console.log") {
		t.Errorf("steer rules = %+v", md.SteerRules)
	}
	if len(md.Directives) != 3 {
		t.Fatalf("expected 3 directives, got %+v", md.Directives)
	}
	if md.Directives[0].Section != "Conventions" {
		t.Errorf("section = %q", md.Directives[0].Section)
	}

	missing, err := ExtractInstructionFile(harness.ClaudeCode, filepath.Join(dir, "nope.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.SteerRules) != 0 || len(missing.Directives) != 0 {
		t.Error("missing file should yield empty result")
	}
}

// fakeAdapter replays prepared events, so a coverage test needs no second
// on-disk transcript format.
type fakeAdapter struct {
	id     harness.ID
	events []transcript.Event
}

func (f fakeAdapter) Harness() harness.ID   { return f.id }
func (f fakeAdapter) Root() (string, error) { return "/fake", nil }

func (f fakeAdapter) Discover(string, transcript.Filter) ([]transcript.Session, error) {
	return []transcript.Session{{Harness: f.id, Path: "/fake/a.jsonl", Project: "/repo/alpha"}}, nil
}

func (f fakeAdapter) Stream(_ transcript.Session, _ transcript.Filter, emit func(transcript.Event) error) (transcript.Counts, error) {
	for _, e := range f.events {
		if err := emit(e); err != nil {
			return transcript.Counts{}, err
		}
	}
	return transcript.Counts{Entries: len(f.events), Bytes: 10}, nil
}

func TestDigestReportsCoveragePerHarness(t *testing.T) {
	codexEvents := []transcript.Event{
		{Kind: transcript.KindHumanTurn, SessionID: "root-1", Text: "stop adding docstrings"},
		{Kind: transcript.KindHumanTurn, SessionID: "root-1", ThreadID: "thread-9", Text: "why do you keep using sed"},
	}
	claudeEvents := []transcript.Event{
		{Kind: transcript.KindHumanTurn, SessionID: "cc-1", Text: "I said use the logger"},
	}
	adapters := []transcript.Adapter{
		fakeAdapter{id: harness.ClaudeCode, events: claudeEvents},
		fakeAdapter{id: harness.Codex, events: codexEvents},
	}

	scanner, projects := Scan(adapters, time.Time{}, transcript.Filter{AllProjects: true}, nil)
	d := scanner.Finalize(30, projects, nil)

	if d.DigestVersion != 2 {
		t.Errorf("digest_version = %d; want 2", d.DigestVersion)
	}
	// Two root sessions and one thread: a thread is provenance, not a unit.
	if d.Stats.SessionsScanned != 2 || d.Stats.ThreadsScanned != 1 {
		t.Errorf("stats = %+v", d.Stats)
	}
	if len(d.Stats.Harnesses) != 2 {
		t.Fatalf("harness coverage = %+v", d.Stats.Harnesses)
	}
	// Registry order, whatever order the scan ran in.
	if d.Stats.Harnesses[0].Harness != harness.ClaudeCode || d.Stats.Harnesses[1].Harness != harness.Codex {
		t.Fatalf("coverage order = %+v", d.Stats.Harnesses)
	}
	if d.Stats.Harnesses[1].SessionsScanned != 1 || d.Stats.Harnesses[1].ThreadsScanned != 1 {
		t.Errorf("codex coverage = %+v", d.Stats.Harnesses[1])
	}
	if d.Stats.Harnesses[0].ThreadsScanned != 0 {
		t.Errorf("claude coverage = %+v", d.Stats.Harnesses[0])
	}
	if len(d.Signals.UserCorrections) != 3 {
		t.Errorf("both harnesses should contribute corrections: %d", len(d.Signals.UserCorrections))
	}
}

func TestScannerAttributesFailuresByShellFlag(t *testing.T) {
	events := []transcript.Event{
		{Kind: transcript.KindToolCall, SessionID: "s", ToolUseID: "t1", Tool: "exec_command", Target: "go test ./...", Shell: true},
		{Kind: transcript.KindToolResult, SessionID: "s", ToolUseID: "t1", Text: "FAIL: alpha_test.go:14"},
		// A non-shell tool reading a file full of failure text is not a failure.
		{Kind: transcript.KindToolCall, SessionID: "s", ToolUseID: "t2", Tool: "apply_patch", Target: "log.txt"},
		{Kind: transcript.KindToolResult, SessionID: "s", ToolUseID: "t2", Text: "FAIL: alpha_test.go:14"},
	}
	scanner, projects := Scan([]transcript.Adapter{fakeAdapter{id: harness.Codex, events: events}}, time.Time{}, transcript.Filter{AllProjects: true}, nil)
	d := scanner.Finalize(30, projects, nil)

	if len(d.Signals.ToolFailures) != 1 {
		t.Fatalf("tool failures = %+v", d.Signals.ToolFailures)
	}
	if !strings.Contains(d.Signals.ToolFailures[0].Examples[0], "go test ./...") {
		t.Errorf("failure example should name the command: %+v", d.Signals.ToolFailures[0].Examples)
	}
}

// emptyAdapter reaches its root cleanly and finds nothing there.
type emptyAdapter struct{ id harness.ID }

func (e emptyAdapter) Harness() harness.ID   { return e.id }
func (e emptyAdapter) Root() (string, error) { return "/fake", nil }

func (e emptyAdapter) Discover(string, transcript.Filter) ([]transcript.Session, error) {
	return nil, nil
}

func (e emptyAdapter) Stream(transcript.Session, transcript.Filter, func(transcript.Event) error) (transcript.Counts, error) {
	return transcript.Counts{}, nil
}

// unreachableAdapter cannot resolve a transcript root at all.
type unreachableAdapter struct{ id harness.ID }

func (u unreachableAdapter) Harness() harness.ID   { return u.id }
func (u unreachableAdapter) Root() (string, error) { return "", os.ErrNotExist }

func (u unreachableAdapter) Discover(string, transcript.Filter) ([]transcript.Session, error) {
	return nil, nil
}

func (u unreachableAdapter) Stream(transcript.Session, transcript.Filter, func(transcript.Event) error) (transcript.Counts, error) {
	return transcript.Counts{}, nil
}

func TestCoverageSeparatesCleanScansFromUnreachableHarnesses(t *testing.T) {
	// Reached but empty: a row of zeros.
	scanner, projects := Scan([]transcript.Adapter{emptyAdapter{id: harness.Codex}}, time.Time{}, transcript.Filter{AllProjects: true}, nil)
	d := scanner.Finalize(30, projects, nil)
	if len(d.Stats.Harnesses) != 1 {
		t.Fatalf("a reached harness must report a row: %+v", d.Stats.Harnesses)
	}
	got := d.Stats.Harnesses[0]
	if got.Harness != harness.Codex || got.FilesScanned != 0 || got.SessionsScanned != 0 {
		t.Errorf("clean scan row = %+v; want zeros", got)
	}

	// Never reached: no row at all.
	scanner, projects = Scan([]transcript.Adapter{unreachableAdapter{id: harness.Codex}}, time.Time{}, transcript.Filter{AllProjects: true}, nil)
	d = scanner.Finalize(30, projects, nil)
	if len(d.Stats.Harnesses) != 0 {
		t.Errorf("an unreachable harness must report no row: %+v", d.Stats.Harnesses)
	}
}

func TestCoverageCountsEachHarnessOnItsOwnIDs(t *testing.T) {
	// Both harnesses use the id "s1". Neither may swallow the other's session.
	shared := []transcript.Event{{Kind: transcript.KindHumanTurn, SessionID: "s1", ThreadID: "t1", Text: "I said use the logger"}}
	adapters := []transcript.Adapter{
		fakeAdapter{id: harness.ClaudeCode, events: shared},
		fakeAdapter{id: harness.Codex, events: shared},
	}

	scanner, projects := Scan(adapters, time.Time{}, transcript.Filter{AllProjects: true}, nil)
	d := scanner.Finalize(30, projects, nil)

	for _, cov := range d.Stats.Harnesses {
		if cov.SessionsScanned != 1 || cov.ThreadsScanned != 1 {
			t.Errorf("%s coverage = %+v; want 1 session and 1 thread", cov.Harness, cov)
		}
	}
	// The scanner-wide totals still deduplicate: one id is one session.
	if d.Stats.SessionsScanned != 1 || d.Stats.ThreadsScanned != 1 {
		t.Errorf("totals = %+v", d.Stats)
	}
}
