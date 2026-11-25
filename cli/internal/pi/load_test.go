package pi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// startSession loads the bridge at path in node and fires its session_start
// handlers, as Pi does when a session starts.
func startSession(t *testing.T, node, path string) {
	t.Helper()
	startSessionAfter(t, node, path, "")
}

// startSessionAfter loads the bridge at path in node, runs the script between, then
// calls the factory and fires its session_start handlers, as Pi does when it reuses
// an imported module for /new, /resume or /fork.
func startSessionAfter(t *testing.T, node, path, between string) {
	t.Helper()
	script := `
const { pathToFileURL } = await import("node:url");
const mod = await import(pathToFileURL(process.argv[1]).href);
` + between + `
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
	// A record Steer cannot parse, or one naming no digest, may name the gate bridge,
	// so its load is unknown.
	broken := filepath.Join(LoadDir(), "broken.json")
	for _, record := range []string{`{"path": `, `{"path": "` + gate + `"}`} {
		if err := os.WriteFile(broken, []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := LoadOf(gate); got.Known {
			t.Errorf("beside the record %s: %+v, want unknown", record, got)
		}
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterCheckRunner(harness.GlobalScope, home, "steer check hook --harness pi", 90); err != nil {
		t.Fatal(err)
	}
	if got := LoadOf(checks); !got.Loaded || got.Current {
		t.Errorf("after a rewrite: %+v, want loaded and not current", got)
	}

	// A session start records the bytes Pi imported, not a rewrite that landed after
	// the import.
	startSessionAfter(t, node, checks, `(await import("node:fs")).appendFileSync(process.argv[1], "// rewritten\n");`)
	if got := LoadOf(checks); !got.Loaded || got.Current {
		t.Errorf("a rewrite after the import: %+v, want loaded and not current", got)
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

// A gate or guard reply's operator line reaches stderr when Pi has no UI, as in
// pi -p, where ctx.ui.notify shows nothing.
func TestBridgeOperatorLineReachesStderrWithoutAUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	const line = "[steer] operator line"
	reply := "import json, sys\nsys.stdin.read()\nprint(json.dumps({\"systemMessage\": \"" + line +
		"\", \"block\": True, \"reason\": \"r\"}))\n"
	for _, c := range []struct {
		name     string
		register func(t *testing.T, home string) string
	}{
		{"gate", func(t *testing.T, home string) string {
			steer := filepath.Join(home, "steer")
			if err := os.WriteFile(steer, []byte("#!/usr/bin/env python3\n"+reply), 0o755); err != nil {
				t.Fatal(err)
			}
			path, err := RegisterCommitGateAndResolver(harness.GlobalScope, home, steer+" commit-gate review-pi", 30,
				steer+" commit-gate backfill", 30)
			if err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"guards", func(t *testing.T, home string) string {
			master, err := hookstore.MasterPathIn(harness.GlobalScope, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(master), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(master, []byte(reply), 0o644); err != nil {
				t.Fatal(err)
			}
			path, err := RegisterMaster(harness.GlobalScope, home)
			if err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := isolateHome(t)
			path := c.register(t, home)
			script := `
const { pathToFileURL } = await import("node:url");
const mod = await import(pathToFileURL(process.argv[1]).href);
const handlers = {};
mod.default({ on: (event, fn) => (handlers[event] ??= []).push(fn), sendMessage() {} });
const sessionManager = { getSessionId: () => "s", getSessionFile: () => "", getBranch: () => [] };
for (const fn of handlers.tool_call ?? []) {
  await fn({ toolName: "bash", toolCallId: "c1", input: { command: "git commit -m x" } },
    { cwd: process.cwd(), hasUI: false, sessionManager, model: { id: "m" } });
}
`
			cmd := exec.Command(node, "--input-type=module", "-e", script, path)
			cmd.Dir = home
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("node: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), line) {
				t.Errorf("stderr = %q, want the operator line %q", stderr.String(), line)
			}
		})
	}
}
