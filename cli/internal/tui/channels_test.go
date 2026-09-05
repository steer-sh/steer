package tui

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestChannelRowsNameTheirHarness pins that two commit-gate rows of one scope are
// told apart on screen. The posture carries one row per harness, and enter toggles
// the row's own harness, so rows that render identically let a reader arm a
// harness they did not choose.
func TestChannelRowsNameTheirHarness(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true},
				{Harness: "codex", Scope: "global"},
				{Harness: "pi", Scope: "global"},
			},
			Resolver: []status.ScopeArming{{Harness: "codex", Scope: "global"}},
		},
	}

	m.active = panelChannels
	view := m.View()
	for _, want := range []string{"Claude Code", "Codex", "Pi"} {
		if !strings.Contains(view, want) {
			t.Errorf("the channels panel does not name %q", want)
		}
	}

	rows := m.channelRows()
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.name()] {
			t.Fatalf("two channel rows share the name %q", r.name())
		}
		seen[r.name()] = true
	}

	// The confirmation names the same row the reader selected.
	m.cursor[panelChannels] = 1
	if got := rows[1].name(); !strings.Contains(got, "Codex") {
		t.Errorf("row name %q does not name its harness", got)
	}
}
