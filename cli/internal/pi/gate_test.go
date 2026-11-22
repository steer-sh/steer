package pi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
)

func TestCommitGateBridgeRoundTrip(t *testing.T) {
	home := isolateHome(t)
	path, err := RegisterCommitGateAndResolver(harness.GlobalScope, ".",
		"/opt/steer commit-gate review-pi", 360,
		"/opt/steer commit-gate backfill --if-due --apply --quiet", 30)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "pi-home", "extensions", gateBridgeName)
	if path != want {
		t.Fatalf("bridge path = %q, want %q", path, want)
	}
	gate := CommitGateArming(harness.GlobalScope, ".")
	if !gate.Registered || gate.Timeout != 360 || gate.Command != "/opt/steer commit-gate review-pi" {
		t.Fatalf("gate arming = %#v", gate)
	}
	resolver := ResolverArming(harness.GlobalScope, ".")
	if !resolver.Registered || resolver.Timeout != 30 ||
		resolver.Command != "/opt/steer commit-gate backfill --if-due --apply --quiet" {
		t.Fatalf("resolver arming = %#v", resolver)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"channel":"commit-gate"`, `pi.on("tool_call"`, `pi.on("agent_before_settle"`,
		`session_id: ctx.sessionManager.getSessionId()`, `detached: true`,
		`pi.sendMessage(`, `{ deliverAs: "steer" }`, `return { block: true, reason: reply.reason }`,
		`pi.on("before_agent_start"`, `sections["steer-commit-gate"] = STEER_GUIDANCE`,
		`const STEER_GUIDANCE = "[commit-gate] Steer reviews`, `payload.steer_batch = batchOf(`,
		`sourceInfo?.path === ` + "`builtin:${tool?.name}`",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("generated bridge is missing %q", want)
		}
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("gate bridge still exists after removal: %v", err)
	}
}

// The gate bridge names the turn by its newest user message, at a commit and at the
// settle boundary, and refuses a settle the backstop blocks with a continuing entry.
// The session's record of that entry marks the next settle of the turn as already
// refused, and a new user message starts a turn of its own. Only an aborted run, which
// Pi settles without that boundary, runs the resolver when it settles.
func TestTheSettleBackstopRefusesOncePerTurn(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	home := isolateHome(t)
	payloads := filepath.Join(home, "payloads.jsonl")
	steer := filepath.Join(home, "steer")
	fake := "#!/usr/bin/env python3\nimport json, sys\nraw = sys.stdin.read()\n" +
		"open(" + strconv.Quote(payloads) + ", 'a').write(json.dumps(raw) + '\\n')\n" +
		"p = json.loads(raw)\n" +
		"if p.get('hook_event_name') == 'Stop' and p.get('stop_hook_active') is False:\n" +
		"    print(json.dumps({'decision': 'block', 'reason': 'owed ' + p['turn_id']}))\n"
	if err := os.WriteFile(steer, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := RegisterCommitGateAndResolver(harness.GlobalScope, home, steer+" commit-gate review-pi", 30,
		steer+" commit-gate backfill", 30)
	if err != nil {
		t.Fatal(err)
	}
	script := `
const { pathToFileURL } = await import("node:url");
const mod = await import(pathToFileURL(process.argv[1]).href);
const handlers = {};
mod.default({ on: (event, fn) => (handlers[event] ??= []).push(fn), sendMessage() {} });
let branch = [
  { type: "message", id: "u1", message: { role: "user", content: "commit it" } },
  { type: "message", id: "a1", message: { role: "assistant", content: [] } },
];
const ctx = { cwd: process.cwd(), hasUI: false, model: { id: "m" },
  sessionManager: { getSessionId: () => "s", getSessionFile: () => "/s.jsonl", getBranch: () => branch } };
const settle = async () => {
  const draft = { type: "custom_message", customType: "steer-check", content: "c", display: true };
  return await handlers.agent_before_settle[0]({ outcome: "completed", entries: [draft], continue: false }, ctx);
};
const out = [];
await handlers.tool_call[0]({ toolName: "bash", toolCallId: "c1", input: { command: "git commit -m x" } }, ctx);
const first = await settle();
out.push(first);
branch = [...branch, ...first.entries.slice(1), { type: "message", id: "a2", message: { role: "assistant", content: [] } }];
out.push(await settle() ?? null);
branch = [...branch, { type: "message", id: "u2", message: { role: "user", content: "again" } }];
out.push(await settle());
for (const aborted of [false, true]) await handlers.agent_settled[0]({ aborted }, ctx);
console.log(JSON.stringify(out));
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, path)
	cmd.Dir = home
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	type entry struct {
		Type, CustomType, Content string
	}
	var results []*struct {
		Entries  []entry
		Continue bool
	}
	if err := json.Unmarshal(stdout, &results); err != nil || len(results) != 3 {
		t.Fatalf("settle results = %s (%v)", stdout, err)
	}
	refusal := func(turn string) []entry {
		return []entry{{"custom_message", "steer-check", "c"}, {"custom_message", "steer-ledger-backstop", "owed " + turn}}
	}
	if r := results[0]; r == nil || !r.Continue || !reflect.DeepEqual(r.Entries, refusal("u1")) {
		t.Errorf("first settle = %+v, want the backstop's entry after the earlier draft and a continue", r)
	}
	if results[1] != nil {
		t.Errorf("second settle of the turn = %+v, want no refusal", results[1])
	}
	if r := results[2]; r == nil || !r.Continue || !reflect.DeepEqual(r.Entries, refusal("u2")) {
		t.Errorf("settle of a new turn = %+v, want a refusal naming u2", r)
	}

	// The aborted run's resolver is detached and writes its record after node exits.
	var lines []string
	for deadline := time.Now().Add(5 * time.Second); len(lines) < 5 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		raw, _ := os.ReadFile(payloads)
		lines = strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
	time.Sleep(200 * time.Millisecond)
	raw, _ := os.ReadFile(payloads)
	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var stdin string
		var p map[string]any
		if json.Unmarshal([]byte(line), &stdin) != nil || (stdin != "" && json.Unmarshal([]byte(stdin), &p) != nil) {
			t.Fatalf("payload %q", line)
		}
		if stdin == "" {
			got = append(got, map[string]any{"event": "detached"})
			continue
		}
		got = append(got, map[string]any{"event": p["hook_event_name"], "turn": p["turn_id"], "active": p["stop_hook_active"]})
	}
	want := []map[string]any{
		{"event": "PreToolUse", "turn": "u1", "active": nil},
		{"event": "Stop", "turn": "u1", "active": false},
		{"event": "Stop", "turn": "u1", "active": true},
		{"event": "Stop", "turn": "u2", "active": false},
		{"event": "detached"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payloads = %v, want %v", got, want)
	}
}

func TestGateAndGuardBridgesAreIndependent(t *testing.T) {
	isolateHome(t)
	guardPath, err := RegisterMaster(harness.GlobalScope, ".")
	if err != nil {
		t.Fatal(err)
	}
	gatePath, err := RegisterCommitGateAndResolver(harness.GlobalScope, ".",
		"steer commit-gate review-pi", 360, "steer commit-gate backfill", 30)
	if err != nil {
		t.Fatal(err)
	}
	if guardPath == gatePath {
		t.Fatal("both channels registered into one file")
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(guardPath); err != nil {
		t.Fatalf("removing the gate removed the guard bridge: %v", err)
	}
}

func TestGateRemovalRefusesAnUnstampedExtension(t *testing.T) {
	isolateHome(t)
	dir, err := ExtensionsDir(harness.GlobalScope, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, gateBridgeName)
	if err := os.WriteFile(path, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err == nil {
		t.Fatal("removed an unstamped Pi extension")
	}
}
