package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/steer-sh/steer-cli/internal/status"
)

// Set STEER_TUI_PREVIEW_DIR to export representative ANSI frames for visual review.
func TestStylingPreviews(t *testing.T) {
	dir := os.Getenv("STEER_TUI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("visual fixture export is opt-in")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	dark := lipgloss.HasDarkBackground()
	defer lipgloss.SetHasDarkBackground(dark)
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := geometryModel(t)
	m.width = 120
	m.height = 40
	m.snap.Posture.CommitGate = []status.ScopeArming{
		{Harness: "claude-code", Scope: "project", Registered: true, Effective: true},
		{Harness: "claude-code", Scope: "global"},
		{Harness: "codex", Scope: "project", Registered: true, Resolvable: status.ResolveMissing},
	}
	m.snap.Hooks = m.snap.Hooks[:3]
	m.snap.Interventions = m.snap.Interventions[:2]
	m.cfg.ReviewerModel = "model supplied by the reviewer CLI"
	for _, mode := range []struct {
		name string
		dark bool
	}{{"dark", true}, {"light", false}} {
		lipgloss.SetHasDarkBackground(mode.dark)
		for i, p := range []panel{panelChannels, panelProblems, panelFixes, panelReviewer, panelProblems, panelDiagnostics} {
			m.active = p
			m.expanded = i == 4
			m.technical = i == 5
			if err := os.WriteFile(filepath.Join(dir, mode.name+"-"+string(rune('1'+i))+".ansi"), []byte(m.View()), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
