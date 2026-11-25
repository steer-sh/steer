//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SeedClaudeSession writes a Claude Code transcript launched in cwd, under the
// project directory Claude Code names after that path.
func (s *Sandbox) SeedClaudeSession(session, cwd string) {
	s.t.Helper()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	s.writeTranscript(filepath.Join(s.Home, ".claude", "projects", slug, session+".jsonl"), []map[string]any{
		{"type": "user", "sessionId": session, "cwd": cwd, "timestamp": ts,
			"message": map[string]any{"role": "user", "content": "stop adding docstrings"}},
		{"type": "assistant", "sessionId": session, "cwd": cwd, "timestamp": ts,
			"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}}},
	})
}

// SeedCodexSession writes a Codex root rollout launched in cwd into today's
// partition of $CODEX_HOME/sessions. thread is the 12-digit tail of its id.
func (s *Sandbox) SeedCodexSession(thread int, cwd string) {
	s.t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("0199a000-0000-7000-8000-%012d", thread)
	ts := now.Format("2006-01-02T15:04:05.000Z")
	message := func(role, kind, text, item string) map[string]any {
		return map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "message", "role": role, "content": []any{map[string]any{"type": kind, "text": text}},
			"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{item}}}}
	}
	s.writeTranscript(filepath.Join(s.Home, ".codex", "sessions", now.Format("2006/01/02"),
		"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+id+".jsonl"), []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": id, "session_id": id, "cwd": cwd, "originator": "codex-tui", "thread_source": "user"}},
		message("user", "input_text", "stop adding docstrings", "user.text"),
		message("assistant", "output_text", "ok", "unknown"),
	})
}

// reportSessions runs `steer report` in dir with args and returns the sessions it
// scanned per harness.
func (s *Sandbox) reportSessions(dir string, args ...string) map[string]int {
	s.t.Helper()
	r := s.Run(dir, "", []string{"PWD=" + dir}, "steer", append([]string{"report"}, args...)...)
	if r.Code != 0 {
		s.t.Fatalf("steer report %q in %s exited %d:\n%s", args, dir, r.Code, r.Output())
	}
	var index struct {
		Stats struct {
			Harnesses []struct {
				Harness  string `json:"harness"`
				Sessions int    `json:"sessions_scanned"`
			} `json:"harnesses"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &index); err != nil {
		s.t.Fatalf("steer report %q: %v\n%s", args, err, r.Stdout)
	}
	got := map[string]int{}
	for _, h := range index.Stats.Harnesses {
		got[h.Harness] = h.Sessions
	}
	return got
}

// TestReportScopesSessionsToTheCheckout checks which launch directories each scan
// scope reads, for Claude Code and Codex sessions alike: the checkout, a
// subdirectory, a nested repository, a sibling directory and a symlink to the checkout.
//
// pins existing behaviour
func TestReportScopesSessionsToTheCheckout(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	sub := filepath.Join(s.Repo, "sub")
	nested := filepath.Join(s.Repo, "nested")
	other := s.path("other")
	link := s.path("link")
	for _, dir := range []string{sub, nested, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.Run(nested, "", nil, "git", "init", "-q"); r.Code != 0 {
		t.Fatalf("git init nested: %s", r.Output())
	}
	mustLink(t, s.Repo, link)
	for i, cwd := range []string{s.Repo, sub, nested, other, link} {
		s.SeedClaudeSession(fmt.Sprintf("session-%d", i), cwd)
		s.SeedCodexSession(i+1, cwd)
	}

	for _, tc := range []struct {
		name string
		dir  string
		args []string
		want int
	}{
		{"all projects", s.Repo, nil, 5},
		{"project-only in the repository", s.Repo, []string{"--project-only"}, 1},
		{"project-only through the symlink", link, []string{"--project-only"}, 2},
		{"repo-only in the repository", s.Repo, []string{"--repo-only"}, 3},
		{"repo-only from a subdirectory", sub, []string{"--repo-only"}, 3},
	} {
		got := s.reportSessions(tc.dir, tc.args...)
		for _, h := range []string{"claude-code", "codex"} {
			if got[h] != tc.want {
				t.Errorf("%s: %s sessions = %d, want %d (all: %v)", tc.name, h, got[h], tc.want, got)
			}
		}
	}

	if r := s.Run(other, "", nil, "steer", "report", "--repo-only"); r.Code == 0 || !strings.Contains(r.Output(), "--repo-only needs a repository") {
		t.Errorf("repo-only outside a repository exited %d:\n%s", r.Code, r.Output())
	}
}
