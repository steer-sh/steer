package pi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// startSession loads the bridge at path in node and fires its session_start
// handlers, as Pi does when a session starts.
func startSession(t *testing.T, node, path string) {
	t.Helper()
	script := `
const { pathToFileURL } = await import("node:url");
const mod = await import(pathToFileURL(process.argv[1]).href);
const handlers = {};
mod.default({ on: (event, fn) => (handlers[event] ??= []).push(fn), sendMessage() {} });
for (const fn of handlers.session_start ?? []) await fn({ reason: "startup" }, { cwd: process.cwd(), hasUI: false });
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

// A generated bridge records its load at session start, so Steer reads a bridge Pi
// loaded as loaded and current, one rewritten since as loaded and not current, and
// one no session loaded as not loaded.
func TestBridgeRecordsTheBytesPiLoaded(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	home := isolateHome(t)

	checks, err := RegisterCheckRunner(harness.GlobalScope, home, "steer check hook --harness pi", 60)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := RegisterCommitGateAndResolver(harness.GlobalScope, home, "steer commit-gate review-pi", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30)
	if err != nil {
		t.Fatal(err)
	}
	if got := LoadOf(checks); !got.Known || got.Loaded {
		t.Fatalf("before any session: %+v, want known and not loaded", got)
	}

	startSession(t, node, checks)
	if got := LoadOf(checks); !got.Known || !got.Loaded || !got.Current {
		t.Errorf("after a session: %+v, want loaded and current", got)
	}
	if got := LoadOf(gate); !got.Known || got.Loaded {
		t.Errorf("a bridge no session loaded: %+v, want known and not loaded", got)
	}

	if _, err := RegisterCheckRunner(harness.GlobalScope, home, "steer check hook --harness pi", 90); err != nil {
		t.Fatal(err)
	}
	if got := LoadOf(checks); !got.Loaded || got.Current {
		t.Errorf("after a rewrite: %+v, want loaded and not current", got)
	}

	// A committed project bridge records its load in the home of whoever runs Pi,
	// not in the home of the user who wrote it.
	repo := t.TempDir()
	project, err := RegisterMaster(harness.ProjectScope, repo)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), home) {
		t.Errorf("the project bridge names the home that wrote it:\n%s", content)
	}
	isolateHome(t)
	startSession(t, node, project)
	if got := LoadOf(project); !got.Loaded || !got.Current {
		t.Errorf("a project bridge loaded under another home: %+v, want loaded and current", got)
	}
}

// ProjectTrust takes the nearest saved decision from the project up through its
// parents, as Pi does, and reads a project no decision covers as undecided.
func TestProjectTrustTakesTheNearestDecision(t *testing.T) {
	home := isolateHome(t)
	parent := filepath.Join(home, "work")
	project := filepath.Join(parent, "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(home, "pi-home", "trust.json")
	write := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(trust), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(trust, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := ProjectTrust(project); got != TrustUndecided {
		t.Errorf("no trust file: %v, want undecided", got)
	}
	for _, c := range []struct {
		body string
		want Trust
	}{
		{`{"` + filepath.Dir(resolved) + `": true}`, TrustGranted},
		{`{"` + filepath.Dir(resolved) + `": true, "` + resolved + `": false}`, TrustDeclined},
		{`{"` + resolved + `": null, "/elsewhere": true}`, TrustUndecided},
		{`not json`, TrustUnread},
	} {
		write(c.body)
		if got := ProjectTrust(project); got != c.want {
			t.Errorf("trust.json %s: %v, want %v", c.body, got, c.want)
		}
	}
}

// RefreshBridges rewrites a bridge an older Steer stamped with the current body and
// the command and timeout it held, and leaves a current bridge alone.
func TestRefreshBridgesRewritesAnOlderBodyAndKeepsItsCommand(t *testing.T) {
	home := isolateHome(t)
	path, err := RegisterCheckRunner(harness.GlobalScope, home, "steer check hook --harness pi", 75)
	if err != nil {
		t.Fatal(err)
	}
	if paths, err := RefreshBridges(harness.GlobalScope, ""); err != nil || len(paths) != 0 {
		t.Fatalf("a current bridge: rewrote %v, err %v; want none", paths, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(content), `"bridge":"`+CheckBridgeDigest()+`"`, `"bridge":"00000000"`, 1)
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if BridgeCurrent(path) {
		t.Fatal("a bridge stamped with another body reads current")
	}
	paths, err := RefreshBridges(harness.GlobalScope, "")
	if err != nil || len(paths) != 1 || paths[0] != path {
		t.Fatalf("rewrote %v, err %v; want %s", paths, err, path)
	}
	got := CheckRunnerArming(harness.GlobalScope, home)
	if !BridgeCurrent(path) || got.Command != "steer check hook --harness pi" || got.Timeout != 75 {
		t.Errorf("after refresh: current %v, arming %+v", BridgeCurrent(path), got)
	}
}
