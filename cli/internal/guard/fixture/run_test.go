package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const loadWarning = `sys.stderr.write("steer: guard other not loaded: SyntaxError\n")`

const masterDeny = `print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "no"}}))`

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.py")
	if err := os.WriteFile(path, []byte("import json, sys\nsys.stdin.read()\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Stderr neither hides a block on stdout nor stands in for one.
func TestInvokeReadsTheDecisionFromStdoutAlone(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Outcome
	}{
		{"master deny after a load warning", loadWarning + "\n" + masterDeny, OutcomeBlock},
		{"master deny before a warning", masterDeny + "\nsys.stdout.flush()\n" + loadWarning, OutcomeBlock},
		{"guard block after stderr output", `sys.stderr.write("debug\n")` + "\n" + `print(json.dumps({"decision": "block", "stopReason": "no"}))`, OutcomeBlock},
		{"master deny with a non-zero exit", loadWarning + "\n" + masterDeny + "\nsys.exit(3)", OutcomeBlock},
		{"allow whose stderr quotes a block", `sys.stderr.write('{"decision": "block"}\n')`, OutcomeAllow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Runner{}.invoke(writeScript(t, c.body), []byte("{}"), t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("outcome = %q, want %q", got, c.want)
			}
		})
	}
}

// A failure's message carries both streams, since stderr is how it gets diagnosed.
func TestInvokeNamesBothStreamsOnANonZeroExit(t *testing.T) {
	script := writeScript(t, `print("partial reply")`+"\n"+`sys.stderr.write("boom\n")`+"\nsys.exit(3)")
	_, err := Runner{}.invoke(script, []byte("{}"), t.TempDir(), "")
	if err == nil {
		t.Fatal("a non-zero exit with no block reported no error")
	}
	for _, want := range []string{"exited non-zero", "partial reply", "boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
