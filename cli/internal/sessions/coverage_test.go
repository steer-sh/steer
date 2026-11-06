package sessions

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/testhome"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// isolatedProject points both harness roots into a temp home and returns the
// Claude Code project directory sessions are planted in.
func isolatedProject(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	project := filepath.Join(home, ".claude", "projects", "-t-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}

func plant(t *testing.T, project, session string, turns ...string) {
	t.Helper()
	var b bytes.Buffer
	for i, text := range turns {
		ts := time.Now().Add(-time.Duration(len(turns)-i) * time.Minute)
		fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"timestamp":%q,"message":{"content":%q}}`+"\n",
			session, ts.UTC().Format(time.RFC3339), text)
	}
	if err := os.WriteFile(filepath.Join(project, session+".jsonl"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type clusterShape struct {
	Pattern         string
	Count, Sessions int
}

func shapes(cs []*report.Cluster) []clusterShape {
	out := []clusterShape{}
	for _, c := range cs {
		out = append(out, clusterShape{c.Pattern, c.Count, c.Sessions})
	}
	return out
}

// The stored projection groups and ranks the same corrections a live scan does:
// keys that normalize together, a score tie, and a resumed session whose early
// correction falls outside the window.
func TestCorrectionClustersMatchALiveScan(t *testing.T) {
	project := isolatedProject(t)
	plant(t, project, "s1", "don't use var", "Don't use VAR!", "stop adding comments")
	plant(t, project, "s2", "don't use var", "stop adding comments")
	plant(t, project, "s3", "I said use tabs", "I said use spaces")
	resumed := fmt.Sprintf(`{"type":"user","sessionId":"s4","timestamp":%q,"message":{"content":"never use globals"}}`+"\n"+
		`{"type":"user","sessionId":"s4","timestamp":%q,"message":{"content":"ok continue"}}`+"\n",
		time.Now().AddDate(0, 0, -45).UTC().Format(time.RFC3339), time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(project, "s4.jsonl"), []byte(resumed), 0o644); err != nil {
		t.Fatal(err)
	}

	since := time.Now().AddDate(0, 0, -30)
	filter, err := report.ScopeAll.Filter(since)
	if err != nil {
		t.Fatal(err)
	}
	scanner, names := report.Scan(report.Adapters(harness.Unknown), since, filter, nil)
	live := scanner.Finalize(30, names, nil).Signals.UserCorrections

	fresh, err := Build(Options{WindowDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	stored := CorrectionClusters(fresh.Units, filter)

	if len(live) != 4 {
		t.Fatalf("the fixture must yield four live clusters, got %+v", shapes(live))
	}
	if !reflect.DeepEqual(shapes(stored), shapes(live)) {
		t.Fatalf("stored clusters differ from the live scan:\nstored %+v\nlive   %+v", shapes(stored), shapes(live))
	}
}

// A repository scope keeps the stored sessions resolved to that root alone; one
// with no stored root is in no repository scope.
func TestCorrectionClustersSelectsStoredRepo(t *testing.T) {
	sig := []mine.SessionSignal{{Kind: "user_corrections", Text: "don't use var", TS: float64(time.Now().Unix())}}
	units := []mine.SessionUnit{
		{SessionID: "a", Project: "p", RepoRoot: "/r/a", Signals: sig},
		{SessionID: "b", Project: "p", RepoRoot: "/r/b", Signals: sig},
		{SessionID: "c", Project: "p", Signals: sig},
	}
	got := CorrectionClusters(units, transcript.Filter{Since: time.Now().Add(-time.Hour), RepoRoot: "/r/a"})
	if len(got) != 1 || got[0].Sessions != 1 {
		t.Fatalf("only repository a's session counts, got %+v", shapes(got))
	}
}

// A build that cannot read a file it selected keeps the last complete build's
// coverage, and with none before it writes none.
func TestRefreshWritesCoverageOnlyAfterACompleteBuild(t *testing.T) {
	project := isolatedProject(t)
	plant(t, project, "s1", "don't use var")
	dir := t.TempDir()
	if _, err := Refresh(dir, "h", Options{WindowDays: 30}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(CoveragePath(dir, "h"))
	if err != nil {
		t.Fatalf("a complete build must write coverage: %v", err)
	}

	unreadable := filepath.Join(project, "s2.jsonl")
	if err := os.WriteFile(unreadable, []byte("{}\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(unreadable); err == nil {
		t.Skip("this user can read a mode-000 file")
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := Refresh(dir, "h", Options{WindowDays: 30}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(CoveragePath(dir, "h"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("an incomplete build must keep the previous coverage:\nbefore %s\nafter  %s (%v)", before, after, err)
	}

	other := t.TempDir()
	if _, err := Refresh(other, "h", Options{WindowDays: 30}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCoverage(other, "h"); ok {
		t.Fatal("an incomplete first build must write no coverage")
	}
}

func TestCoverageCovers(t *testing.T) {
	now := time.Now()
	all := transcript.Filter{AllProjects: true}
	repo := transcript.Filter{RepoRoot: "/r/a"}
	proj := transcript.Filter{Projects: []string{"/w/p"}}
	cases := []struct {
		name string
		c    Coverage
		f    transcript.Filter
		days int
		want bool
	}{
		{"all covers a repo", Coverage{ScannedAt: now, WindowDays: 30, AllProjects: true}, repo, 30, true},
		{"shorter window", Coverage{ScannedAt: now, WindowDays: 7, AllProjects: true}, all, 30, false},
		{"one harness", Coverage{ScannedAt: now, WindowDays: 30, AllProjects: true, Harness: "codex"}, all, 30, false},
		{"older than the window", Coverage{ScannedAt: now.AddDate(0, 0, -8), WindowDays: 30, AllProjects: true}, all, 7, false},
		{"same repo", Coverage{ScannedAt: now, WindowDays: 30, RepoRoot: "/r/a"}, repo, 30, true},
		{"other repo", Coverage{ScannedAt: now, WindowDays: 30, RepoRoot: "/r/b"}, repo, 30, false},
		{"repo does not cover all", Coverage{ScannedAt: now, WindowDays: 30, RepoRoot: "/r/a"}, all, 30, false},
		{"same project", Coverage{ScannedAt: now, WindowDays: 30, Projects: []string{transcript.NormalizeProject("/w/p")}}, proj, 30, true},
		{"project does not cover all", Coverage{ScannedAt: now, WindowDays: 30, Projects: []string{transcript.NormalizeProject("/w/p")}}, all, 30, false},
	}
	for _, tc := range cases {
		if got := tc.c.Covers(tc.f, tc.days, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A shard that does not read whole loses rows when a refresh saves it, so an
// incomplete refresh over it keeps no earlier coverage.
func TestRefreshDropsCoverageOverAShardThatDoesNotReadWhole(t *testing.T) {
	for name, junk := range map[string]string{"malformed": "{not json\n", "sessionless": "{}\n"} {
		project := isolatedProject(t)
		plant(t, project, "s1", "don't use var")
		dir := t.TempDir()
		if _, err := Refresh(dir, "h", Options{WindowDays: 30}); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(ShardPath(dir, "h"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(junk); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if _, err := LoadShardStrict(dir, "h"); err == nil {
			t.Fatalf("%s: the strict reader must refuse the row", name)
		}
		if err := os.WriteFile(filepath.Join(project, "s2.jsonl"), []byte("{}\n"), 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(filepath.Join(project, "s2.jsonl")); err == nil {
			t.Skip("this user can read a mode-000 file")
		}
		if _, err := Refresh(dir, "h", Options{WindowDays: 30}); err != nil {
			t.Fatal(err)
		}
		if _, ok := LoadCoverage(dir, "h"); ok {
			t.Fatalf("%s: an incomplete refresh over a damaged shard must leave no coverage", name)
		}
	}
}
