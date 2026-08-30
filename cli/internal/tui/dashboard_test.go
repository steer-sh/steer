package tui

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// TestModelRendersEveryPanel checks that each panel renders a non-empty frame
// over a default (empty-machine) snapshot, so no panel view panics or blanks.
func TestModelRendersEveryPanel(t *testing.T) {
	m := New(gate.Defaults(), "global")
	for p := panelChannels; p < panelCount; p++ {
		m.active = p
		if got := m.View(); got == "" {
			t.Fatalf("panel %d rendered an empty frame", p)
		}
	}
}
