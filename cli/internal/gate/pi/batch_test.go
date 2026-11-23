package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// TestRunRefusesACommitThatSharesItsBatchWithAWriter checks that a commit whose
// siblings can change files, including an extension's tool under a read-only
// built-in's name, or that a code-mode script made, is refused before any
// review, and that Pi's own read-only siblings or an unread batch still review.
// Only the code-mode refusal names the codemode "only" mode, which leaves no retry.
func TestRunRefusesACommitThatSharesItsBatchWithAWriter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   string
		refused bool
		// onlyMode: the reason names Pi's codemode "only" mode, where no retry exists.
		onlyMode bool
	}{
		{"bash sibling", `,"steer_batch":{"found":true,"siblings":["bash"]}`, true, false},
		{"custom tool sibling", `,"steer_batch":{"found":true,"siblings":["read","deploy"],"shipped":["read"]}`, true, false},
		{"overridden read sibling", `,"steer_batch":{"found":true,"siblings":["read","ls"],"shipped":["ls"]}`, true, false},
		{"nested code-mode call", `,"parent_tool_call_id":"cm-1"`, true, true},
		{"read-only siblings", `,"steer_batch":{"found":true,"siblings":["read","grep","find","ls"],` +
			`"shipped":["read","grep","find","ls"]}`, false, false},
		{"alone", `,"steer_batch":{"found":true,"siblings":[]}`, false, false},
		{"batch not found", `,"steer_batch":{"found":false}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := stagedRepo(t)
			cfg := gate.Defaults()
			cfg.StateDir = t.TempDir()
			cfg.ReviewerRuns = 1
			payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"pi-session",` +
				`"cwd":` + strconvJSON(repo) + `,"tool_input":{"command":"git commit -m \"add a\""}` +
				tc.extra + `}`
			var stdout bytes.Buffer
			Run(context.Background(), cfg, stubReviewer{}, strings.NewReader(payload), &stdout)
			var out Output
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || !out.Block {
				t.Fatalf("output = %q, err = %v; want a block either way", stdout.String(), err)
			}
			refused := strings.Contains(out.Reason, "run the commit alone")
			_, statErr := os.Stat(cfg.LogFile())
			reviewed := statErr == nil
			if refused != tc.refused || reviewed == tc.refused {
				t.Fatalf("refused = %v, reviewed = %v; want refused = %v\nreason: %s",
					refused, reviewed, tc.refused, out.Reason)
			}
			if onlyMode := strings.Contains(out.Reason, `codemode.mode is "only"`); onlyMode != tc.onlyMode {
				t.Fatalf("names the only mode = %v, want %v\nreason: %s", onlyMode, tc.onlyMode, out.Reason)
			}
		})
	}
}
