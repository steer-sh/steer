package plugindist

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A prompt submitted while the session-start cadence still runs receives the
// cadence's context in that first run, not in a later one.
func TestPiSessionContextReachesAFirstPromptSentDuringCadence(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "pi", "steer-session.ts"))
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := t.TempDir()
	extension := filepath.Join(copyRoot, "pi", "steer-session.ts")
	launcher := filepath.Join(copyRoot, "scripts", "steer")
	for path, body := range map[string]string{
		extension: string(source),
		launcher: "#!/bin/sh\nsleep 0.5\n" +
			`echo '{"hookSpecificOutput":{"additionalContext":"steer: pending work"}}'` + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := `
const { pathToFileURL } = await import("node:url");
const mod = await import(pathToFileURL(process.argv[1]).href);
const handlers = {};
mod.default({ on: (event, fn) => (handlers[event] ??= []).push(fn), sendMessage() {} });
for (const fn of handlers.session_start ?? []) await fn({ reason: "startup" }, { cwd: process.cwd(), hasUI: false });
const messages = [];
for (const fn of handlers.before_agent_start ?? []) {
  const result = await fn({ messageType: "user", prompt: "hi" }, { cwd: process.cwd(), hasUI: false });
  if (result?.message) messages.push(result.message.content);
}
process.stdout.write(JSON.stringify(messages));
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, extension)
	cmd.Dir = copyRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := string(out); got != `["steer: pending work"]` {
		t.Errorf("first run's messages = %s, want the cadence context", got)
	}
}
