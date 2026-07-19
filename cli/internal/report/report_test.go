package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, name string, lines []string) TranscriptFile {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return TranscriptFile{Path: path, ProjectDir: filepath.Base(dir)}
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
	if err := s.ScanFile(tf); err != nil {
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
	if err := s.ScanFile(tf); err != nil {
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
	if err := s.ScanFile(tf); err != nil {
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
	if err := s.ScanFile(tf); err != nil {
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
	if err := s.ScanFile(tf); err != nil {
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

func TestExtractClaudeMD(t *testing.T) {
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

	md, err := ExtractClaudeMD(path)
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

	missing, err := ExtractClaudeMD(filepath.Join(dir, "nope.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.SteerRules) != 0 || len(missing.Directives) != 0 {
		t.Error("missing file should yield empty result")
	}
}

func TestFindTranscriptsMatchesProject(t *testing.T) {
	root := t.TempDir()
	match := filepath.Join(root, "-Users-jeremy-my-proj")
	other := filepath.Join(root, "-Users-jeremy-other")
	for _, d := range []string{match, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := FindTranscripts(root, []string{"/Users/jeremy/my.proj"}, false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ProjectDir != "-Users-jeremy-my-proj" {
		t.Fatalf("expected only matching project, got %+v", files)
	}

	// A symlinked cwd matches via its resolved alias.
	aliased, err := FindTranscripts(root, []string{"/Volumes/Ext/my.proj", "/Users/jeremy/my.proj"}, false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliased) != 1 {
		t.Fatalf("expected alias path to match, got %+v", aliased)
	}

	all, err := FindTranscripts(root, []string{"/Users/jeremy/my.proj"}, true, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 files with --all-projects, got %d", len(all))
	}

	future := time.Now().Add(time.Hour)
	none, err := FindTranscripts(root, []string{"/Users/jeremy/my.proj"}, true, future)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected mtime filter to exclude all files, got %d", len(none))
	}
}
