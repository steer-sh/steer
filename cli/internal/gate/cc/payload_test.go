package cc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Render turns an engine Decision into the PreToolUse reply. The distinction the cases
// below protect: a note-only reply rules on the permission deliberately not at all,
// because an allow would start auto-approving changes that would otherwise be asked
// about.
func TestRender(t *testing.T) {
	cases := []struct {
		name       string
		decision   gate.Decision
		emit       bool
		decisionIs string
		hasContext bool
	}{
		{"deny", gate.Decision{Kind: gate.Deny, Reason: "reviews", SystemMessage: "fired"},
			true, "deny", false},
		{"allow", gate.Decision{Kind: gate.Allow, Reason: "receipt", SystemMessage: "clean"},
			true, "allow", true},
		{"context-only", gate.Decision{Kind: gate.ContextOnly, Reason: "final review",
			SystemMessage: "capped"}, true, "", true},
		{"silent", gate.Decision{Kind: gate.Silent}, false, "", false},
	}
	for _, tc := range cases {
		out, ok := Render(tc.decision)
		if ok != tc.emit {
			t.Errorf("%s: emitted = %v, want %v", tc.name, ok, tc.emit)
			continue
		}
		if !ok {
			continue
		}
		got := ""
		hasContext := false
		if out.HookSpecificOutput != nil {
			got = out.HookSpecificOutput.PermissionDecision
			hasContext = out.HookSpecificOutput.AdditionalContext != ""
		}
		if got != tc.decisionIs {
			t.Errorf("%s: permissionDecision = %q, want %q", tc.name, got, tc.decisionIs)
		}
		if hasContext != tc.hasContext {
			t.Errorf("%s: additionalContext present = %v, want %v", tc.name, hasContext, tc.hasContext)
		}
	}
}

// An operator-only reply must serialise to a system message alone. A stray
// hookSpecificOutput key would reintroduce the decision the branch exists to avoid.
func TestOperatorOnlySerialisesWithoutADecision(t *testing.T) {
	out, _ := Render(gate.Decision{Kind: gate.Silent, LedgerError: errString("disk full")})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hookSpecificOutput") {
		t.Errorf("note-only reply = %s, want a systemMessage alone", raw)
	}
}

// A failure of the ledger append has no disk to record on, so it reaches the operator
// through the one channel that needs none.
func TestLedgerErrorReachesTheOperator(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Silent, LedgerError: errString("disk full")})
	if !ok {
		t.Fatal("a ledger failure must still produce a reply")
	}
	if !strings.Contains(out.SystemMessage, "disk full") {
		t.Errorf("systemMessage = %q, want the failure named", out.SystemMessage)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The rerun note is added only for a command that writes a file, because the deny stops
// the whole command and a retry of its commit half alone exits 128 on the missing path.
func TestRerunNote(t *testing.T) {
	if rerunNote("git "+c+" -m 'x'") != "" {
		t.Error("a command that writes nothing gets no note, so the mandate stays reachable")
	}
	if rerunNote("cat > m.txt <<'MSG'\nx\nMSG\ngit "+c+" -F m.txt") == "" {
		t.Error("the heredoc that writes the message file is exactly the case")
	}
}
