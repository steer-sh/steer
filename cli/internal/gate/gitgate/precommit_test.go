package gitgate

import (
	"errors"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// IndexSource is the only in-band signal a pre-hook gets about the form of the change.
func TestIndexSource(t *testing.T) {
	cases := []struct {
		env  string
		want string
		why  string
	}{
		{"", "default", "no variable means git handed the hook the repo's own index"},
		{".git/index", "index", "the pre-staged form, relative as git writes it"},
		{"/r/.git/index.lock", "index.lock", "the -a form reuses the lock as its temp index"},
		{"/r/.git/next-index-8626.lock", "next-index-8626.lock", "the pathspec form"},
	}
	for _, tc := range cases {
		got := IndexSource(func(string) string { return tc.env })
		if got != tc.want {
			t.Errorf("IndexSource(%q) = %q, want %q — %s", tc.env, got, tc.want, tc.why)
		}
	}
}

// git shows a pre-commit hook's agent and operator text on one stderr, so a receipt
// prints once; a deny keeps both the reviews and the operator line.
func TestRenderPrintsAReceiptOnce(t *testing.T) {
	receipt := render(gate.Decision{Kind: gate.Allow, Receipt: true,
		Reason: "[commit-gate] repo (firing 1): proceeds.", SystemMessage: "[commit-gate] repo: proceeds."})
	if receipt.OperatorText != "" || receipt.AgentText == "" {
		t.Errorf("receipt render = %+v, want the agent copy alone", receipt)
	}
	failed := render(gate.Decision{Kind: gate.Allow, Receipt: true, Reason: "r", SystemMessage: "s",
		LedgerError: errors.New("disk full")})
	if !strings.HasPrefix(failed.OperatorText, "[commit-gate] the adjudication row could NOT be written (disk full)") {
		t.Errorf("ledger failure on a receipt = %q, want the failure line alone", failed.OperatorText)
	}
	deny := render(gate.Decision{Kind: gate.Deny, Reason: "reviews", SystemMessage: "[commit-gate] Commit blocked"})
	if !deny.Block || deny.AgentText != "reviews" || deny.OperatorText != "[commit-gate] Commit blocked" {
		t.Errorf("deny render = %+v, want reviews and the operator line", deny)
	}
}
