package cmd

import (
	"strings"
	"testing"
)

// TestHelpHintNamesTheFailingSubcommand pins that an error on a deep subcommand
// points at that subcommand's help. The root help lists commands, not the flags a
// subcommand refused, so it answers a question the reader did not ask.
func TestHelpHintNamesTheFailingSubcommand(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"commit-gate", "install", "--bogus"}, "steer commit-gate install --help"},
		{[]string{"status"}, "steer status --help"},
		{nil, "steer --help"},
		{[]string{"frobnicate"}, "steer --help"},
	}
	for _, c := range cases {
		if got := helpHint(c.args); !strings.Contains(got, c.want) {
			t.Errorf("helpHint(%v) = %q, want it to name %q", c.args, got, c.want)
		}
	}
}

// TestRootHelpLeadsWithTheCommitGate pins that the root help describes the product
// the README leads with. It described the retired guard-first, Claude-only flow.
func TestRootHelpLeadsWithTheCommitGate(t *testing.T) {
	long := rootCmd.Long
	for _, want := range []string{"commit-gate install", "reviewer.provider", "Codex", "Pi"} {
		if !strings.Contains(long, want) {
			t.Errorf("the root help does not name %q", want)
		}
	}
	if strings.Contains(long, "Claude Code hooks generated") {
		t.Error("the root help still describes the retired guard-first flow")
	}
}

// TestDedentKeepsRelativeIndent pins that an example block loses only the indent
// every line shares, and that a block sharing none is stripped the same way.
func TestDedentKeepsRelativeIndent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\n  steer status\n  steer commit-gate install\n    --scope global\n",
			"steer status\nsteer commit-gate install\n  --scope global"},
		{"\nsteer status\n  steer commit-gate install\n",
			"steer status\n  steer commit-gate install"},
		// A line of spaces is a blank line, at either end and with any shared indent.
		{"\nsteer status\n  \n", "steer status"},
		{"\n  steer status\n    \n", "steer status"},
		{"  \n  steer status", "steer status"},
		{"  \nsteer status", "steer status"},
		// An interior blank line keeps its place and carries no residual indent.
		{"  steer status\n    \n  steer dashboard", "steer status\n\nsteer dashboard"},
		{"", ""},
		{"\n\n", ""},
	}
	for _, c := range cases {
		if got := dedent(c.in); got != c.want {
			t.Errorf("dedent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
