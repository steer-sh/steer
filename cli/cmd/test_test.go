package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ad-hoc `steer test` reads the decision and reason from stdout, and still shows stderr.
func TestRunHookOnceReadsTheDecisionFromStdoutAlone(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantBlock  bool
		wantReason string
	}{
		{"block after stderr output", `sys.stderr.write("noise\n")` + "\n" + `print(json.dumps({"decision": "block", "stopReason": "why"}))`, true, "why"},
		{"deny after stderr output", `sys.stderr.write("noise\n")` + "\n" + `print(json.dumps({"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "why"}}))`, true, "why"},
		{"allow whose stderr quotes a block", `sys.stderr.write('noise {"decision": "block"}\n')`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "guard.py")
			if err := os.WriteFile(path, []byte("import json, sys\nsys.stdin.read()\n"+c.body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			blocked, reason, raw, err := runHookOnce(path, []byte("{}"))
			if err != nil {
				t.Fatal(err)
			}
			if blocked != c.wantBlock || reason != c.wantReason {
				t.Errorf("blocked=%v reason=%q, want %v %q", blocked, reason, c.wantBlock, c.wantReason)
			}
			if !strings.Contains(raw, "noise") {
				t.Errorf("raw output %q lost stderr", raw)
			}
		})
	}
}
