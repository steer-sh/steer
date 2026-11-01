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
	if len(c.Occurrences) != 4 || c.Occurrences[3].Session != "s3" {
		t.Errorf("expected every occurrence with its session, got %+v", c.Occurrences)
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

// A rejection is the error result that opens with Claude Code's decline envelope.
// A successful read of text that quotes the envelope, and an error that mentions
// it mid-output, are not rejections.
func TestRejectionNeedsTheErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s.jsonl", []string{
		toolUseEntry("s1", "t1", "Bash", "sed -n 1,40p scan.go"),
		toolResultEntry("s1", "t1", "The user doesn't want to proceed with this tool use.", false),
		toolUseEntry("s1", "t2", "Bash", "./run.sh"),
		toolResultEntry("s1", "t2", "error: fixture says the user doesn't want to proceed with this tool use", true),
		toolUseEntry("s1", "t3", "Bash", "rm -rf build"),
		toolResultEntry("s1", "t3", "The user doesn't want to proceed with this tool use. The tool use was rejected.", true),
	})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)

	if len(d.Signals.UserRejections) != 1 || !strings.Contains(d.Signals.UserRejections[0].Pattern, "rm") {
		t.Errorf("expected only the declined rm call, got %+v", d.Signals.UserRejections)
	}
}

// A harness wraps a guard's reason in a sentence of its own — Codex does, Pi
// does not — and that sentence is the same for every guard, so it must not
// reach the key. These are the wrappings, not one harness each: Claude Code
// discards the reason outright and its result carries no footer to key on.
func TestHookBlockKeyIgnoresTheHarnessWrapping(t *testing.T) {
	const footer = "\n\n— steer hook: python3-not-python-hook · remove: steer remove python3-not-python-hook"
	reason := "Bare `python` invocation detected in a Bash command." + footer

	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s.jsonl", []string{
		toolUseEntry("s1", "t1", "Bash", "python -c 'print(1)'"),
		toolResultEntry("s1", "t1", "Hook PreToolUse:Bash denied this tool\n"+reason, true),
		toolUseEntry("s1", "t2", "Bash", "python -c 'print(2)'"),
		toolResultEntry("s1", "t2", reason, true),
		toolUseEntry("s1", "t3", "Bash", "python -c 'print(3)'"),
		toolResultEntry("s1", "t3", "Script failed\nScript error:\nCommand blocked by PreToolUse hook: "+reason, true),
	})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)

	if len(d.Signals.HookBlocks) != 1 {
		t.Fatalf("expected the three wrappings to share one cluster, got %+v", d.Signals.HookBlocks)
	}
	if got := d.Signals.HookBlocks[0].Count; got != 3 {
		t.Errorf("cluster count = %d; want 3", got)
	}
	// The pattern is the normalized cluster key, so the guard's name reaches it
	// with its hyphens dropped.
	if !strings.Contains(d.Signals.HookBlocks[0].Pattern, "python3 not python hook") {
		t.Errorf("cluster key names no guard: %q", d.Signals.HookBlocks[0].Pattern)
	}
}

// Claude Code records a block as its wrapper sentence alone, dropping the
// guard's reason and with it the footer. The fallback test is what keeps that
// block a signal at all, and its key can name no guard.
func TestHookBlockKeepsTheFooterlessClaudeWrapper(t *testing.T) {
	dir := t.TempDir()
	tf := writeTranscript(t, dir, "s.jsonl", []string{
		toolUseEntry("s1", "t1", "Bash", "python -c 'print(1)'"),
		toolResultEntry("s1", "t1", "Hook PreToolUse:Bash denied this tool", true),
	})

	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	d := s.Finalize(30, nil, nil)

	if len(d.Signals.HookBlocks) != 1 {
		t.Fatalf("expected the wrapper alone to stay a hook block, got %+v", d.Signals.HookBlocks)
	}
	if strings.Contains(d.Signals.HookBlocks[0].Pattern, "steer hook") {
		t.Errorf("key claims a footer the result never carried: %q", d.Signals.HookBlocks[0].Pattern)
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

	md, err := ExtractInstructionFile(harness.ClaudeCode, path, nil)
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

	missing, err := ExtractInstructionFile(harness.ClaudeCode, filepath.Join(dir, "nope.md"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.SteerRules) != 0 || len(missing.Directives) != 0 {
		t.Error("missing file should yield empty result")
	}
}

// A guard and an instruction rule both write a line into the section, so only a
// line whose text is a guard's rule reads as enforced. The same text outside the
// section is a directive, never a guard rule.
func TestOnlyAGuardsLineReadsAsEnforced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	content := "## Conventions\n- use python3 instead\n\n## Steer Rules\n<!-- managed-by-steer -->\n" +
		"- resolve a config path from the repo root\n- use python3 instead\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := ExtractInstructionFile(harness.Codex, path, []string{"use python3 instead", "a guard with no line"})
	if err != nil {
		t.Fatal(err)
	}
	if len(md.SteerRules) != 2 {
		t.Errorf("steer rules = %q", md.SteerRules)
	}
	if len(md.GuardRules) != 1 || md.GuardRules[0] != "use python3 instead" {
		t.Errorf("guard rules = %q, want only the guard's line", md.GuardRules)
	}
	if len(md.Directives) != 1 {
		t.Errorf("directives = %+v", md.Directives)
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
func (f fakeAdapter) Instrument() string    { return string(f.id) + ":fake" }

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

	if d.DigestVersion != 6 {
		t.Errorf("digest_version = %d; want 6", d.DigestVersion)
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
	if !strings.Contains(d.Signals.ToolFailures[0].Occurrences[0].Text, "go test ./...") {
		t.Errorf("failure example should name the command: %+v", d.Signals.ToolFailures[0].Occurrences)
	}
}

// emptyAdapter reaches its root cleanly and finds nothing there.
type emptyAdapter struct{ id harness.ID }

func (e emptyAdapter) Harness() harness.ID   { return e.id }
func (e emptyAdapter) Root() (string, error) { return "/fake", nil }
func (e emptyAdapter) Instrument() string    { return string(e.id) + ":fake" }

func (e emptyAdapter) Discover(string, transcript.Filter) ([]transcript.Session, error) {
	return nil, nil
}

func (e emptyAdapter) Stream(transcript.Session, transcript.Filter, func(transcript.Event) error) (transcript.Counts, error) {
	return transcript.Counts{}, nil
}

// unreachableAdapter cannot resolve a transcript root at all.
type unreachableAdapter struct{ id harness.ID }

func (u unreachableAdapter) Harness() harness.ID   { return u.id }
func (u unreachableAdapter) Instrument() string    { return string(u.id) + ":fake" }
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

// The queue's transcript row takes the head of this order as its examples, so the
// order is a contract with the cadence package and not an internal detail.
//
// pins existing behaviour
func TestCollectRanksBySpreadThenKey(t *testing.T) {
	clusters := map[string]*Cluster{
		"aaa once":       {Pattern: "aaa once", Count: 1, Sessions: 1},
		"zzz everywhere": {Pattern: "zzz everywhere", Count: 9, Sessions: 4},
		"mmm twice":      {Pattern: "mmm twice", Count: 5, Sessions: 2},
		"b tie":          {Pattern: "b tie", Count: 2, Sessions: 2},
		"a tie":          {Pattern: "a tie", Count: 2, Sessions: 2},
	}
	got := collect(clusters)

	want := []string{"zzz everywhere", "mmm twice", "a tie", "b tie", "aaa once"}
	for i, w := range want {
		if got[i].Pattern != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Pattern, w)
		}
	}
}

func TestCorrectionCandidates(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"I said use the logger", "I said use the logger"},
		{"I told you to use pnpm", "I told you to use pnpm"},
		{"I said stop adding comments", "I said stop adding comments"},
		{"I told you not to use mocks here", "I told you not to use mocks here"},
		{"I said I would look at it later", ""},
		{"I told you about this yesterday", ""},
		{"No, the other file", ""},
		{"nope", ""},
		{"wrong branch, try main", ""},
		{"again, please", ""},
		{"STOP.  why are you editorializing the comments", "STOP.  why are you editorializing the comments"},
		{"So why would you have written it without error handling?", "So why would you have written it without error handling?"},
		{"why do you think auto mode works like this", ""},
		{"## Steer Rules\n- never use bare python | use python3 instead", ""},
		{"Rule: don't use bare python", "Rule: don't use bare python"},
		{"- never use npm | use yarn instead\nplease don't use npm here", "please don't use npm here"},
	}
	for _, c := range cases {
		sig, ok := NewClassifier().Classify(transcript.Event{Kind: transcript.KindHumanTurn, Text: c.text})
		if got := map[bool]string{true: sig.Key}[ok]; got != c.want {
			t.Errorf("%q: key %q, want %q", c.text, got, c.want)
		}
	}
}

// A turn that invokes the steer skill hands it a rule; the rule is the request, not a
// correction. A turn that names Steer or another Steer skill is still read.
func TestCorrectionSkipsASteerInvocation(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"$steer:steer never use bare python in bash commands | use python3", ""},
		{"$steer never use bare python", ""},
		{"/steer:steer don't use eval", ""},
		{"/steer\ndon't use eval", ""},
		{"$steer", ""},
		{"$steerage don't use eval", "$steerage don't use eval"},
		{"$steer:steer-report don't use eval", "$steer:steer-report don't use eval"},
		{"/steer-setup never use agy", "/steer-setup never use agy"},
		{"why does $steer:steer keep blocking? don't use it here", "why does $steer:steer keep blocking? don't use it here"},
	}
	for _, c := range cases {
		sig, ok := NewClassifier().Classify(transcript.Event{Kind: transcript.KindHumanTurn, Text: c.text})
		if got := map[bool]string{true: sig.Key}[ok]; got != c.want {
			t.Errorf("%q: key %q, want %q", c.text, got, c.want)
		}
	}
}

// TestCorrectionIgnoresPastedBlocks: text the harness marks as pasted is not what the
// person said, so it raises no correction, and a correction typed beside it still does,
// even when the paste opens the turn.
func TestCorrectionIgnoresPastedBlocks(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"<pasted_content id=\"a1\">\ndon't use var in this file\n</pasted_content>\n\nwhat do you make of this?", ""},
		{"<pasted_content id=\"a1\">\nbuild log\n</pasted_content>\n\nwhy did you add the flag?", "why did you add the flag?"},
		{"read this <pasted_content id=\"b2\">\nnever use mocks here", ""},
		{"don't use var\n<pasted_content id=\"c3\">\nquoted\n</pasted_content>", "don't use var"},
	}
	for _, c := range cases {
		sig, ok := NewClassifier().Classify(transcript.Event{Kind: transcript.KindHumanTurn, Text: c.text})
		if got := map[bool]string{true: sig.Key}[ok]; got != c.want {
			t.Errorf("%q: key %q, want %q", c.text, got, c.want)
		}
	}
}

// TestScannerReadsNoCorrectionInAHeadlessRun: a `claude -p` run's user turn is the
// prompt a program passed, such as a commit review with a diff in it.
func TestScannerReadsNoCorrectionInAHeadlessRun(t *testing.T) {
	dir := t.TempDir()
	headless := `{"type":"user","entrypoint":"sdk-cli","sessionId":"s1","timestamp":"2026-07-10T00:00:00Z","message":{"content":"Review this diff.\n+// don't use the cache here"}}`
	tf := writeTranscript(t, dir, "s.jsonl", []string{headless})
	s := NewScanner(time.Time{})
	if err := s.ScanSession(cc.New(), tf); err != nil {
		t.Fatal(err)
	}
	if got := s.Finalize(30, nil, nil).Signals.UserCorrections; len(got) != 0 {
		t.Fatalf("a headless prompt produced corrections: %+v", got)
	}
}

// A bullet after the section's end marker is the user's directive, not a Steer rule.
func TestExtractInstructionFileStopsAtTheEndMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	content := "## Steer Rules\n<!-- managed-by-steer -->\n- steer rule\n<!-- end-managed-by-steer -->\n\n- my own bullet\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := ExtractInstructionFile(harness.ClaudeCode, path, []string{"my own bullet"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(md.SteerRules, "|") != "steer rule" || len(md.GuardRules) != 0 {
		t.Errorf("steer rules = %q, guard rules = %q", md.SteerRules, md.GuardRules)
	}
	if len(md.Directives) != 1 || md.Directives[0].Text != "my own bullet" || md.Directives[0].Section != "" {
		t.Errorf("directives = %+v", md.Directives)
	}
}

// A bullet whose multi-byte character spans the length limit is cut before that
// character, inside the Steer Rules section and outside it.
func TestExtractInstructionFileCutsOnARuneBoundary(t *testing.T) {
	want := strings.Repeat("a", maxDirectiveLen-1)
	long := want + "\u2014 tail"
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	doc := "# Project\n- " + long + "\n\n## Steer Rules\n<!-- managed-by-steer -->\n- " + long + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := ExtractInstructionFile(harness.Codex, path, []string{long})
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Directives) != 1 || md.Directives[0].Text != want {
		t.Errorf("directives = %q, want the text before the split character", md.Directives)
	}
	if len(md.SteerRules) != 1 || md.SteerRules[0] != want {
		t.Errorf("steer rules = %q, want the text before the split character", md.SteerRules)
	}
	if len(md.GuardRules) != 1 || md.GuardRules[0] != want {
		t.Errorf("guard rules = %q, want the text before the split character", md.GuardRules)
	}
}
