package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

func geometryModel(t *testing.T) *Model {
	t.Helper()
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{}
	m.cfg.ReviewerModel = strings.Repeat("long-model-", 15)
	for i := 0; i < 20; i++ {
		m.snap.Posture.CommitGate = append(m.snap.Posture.CommitGate, status.ScopeArming{Harness: fmt.Sprintf("agent-%02d", i), Scope: "global", Registered: true, Effective: i%3 == 0, Resolvable: status.ResolveMissing})
		m.snap.Hooks = append(m.snap.Hooks, status.HookInfo{Name: fmt.Sprintf("guard-%02d-", i) + strings.Repeat("界-long-", 12)})
		m.snap.Interventions = append(m.snap.Interventions, status.ScoredIntervention{ID: fmt.Sprint(i), Class: fmt.Sprintf("problem-%02d", i), Status: "active", Verdict: "confounded", Tier: "guard", Problem: strings.Repeat("A detailed explanation. ", 40), Target: "last-detail-target"})
	}
	m.snap.Posture.Ledger = "/long/" + strings.Repeat("path/", 30) + "last-diagnostic-content"
	return m
}

func assertFrame(t *testing.T, m *Model) string {
	t.Helper()
	view := m.View()
	if n := lipgloss.Height(view); n > m.height {
		t.Fatalf("panel %d frame height %d > %d", m.active, n, m.height)
	}
	for _, line := range strings.Split(view, "\n") {
		if n := lipgloss.Width(line); n > m.width {
			t.Fatalf("panel %d line width %d > %d: %q", m.active, n, m.width, line)
		}
	}
	return ansi.Strip(view)
}

func TestFrameGeometryAcrossProfilesAndResize(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	dark := lipgloss.HasDarkBackground()
	defer lipgloss.SetHasDarkBackground(dark)
	m := geometryModel(t)
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii} {
		lipgloss.SetColorProfile(profile)
		for _, dark := range []bool{true, false} {
			lipgloss.SetHasDarkBackground(dark)
			for _, size := range [][2]int{{80, 24}, {120, 40}, {180, 50}, {40, 16}, {20, 6}} {
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for p := panelChannels; p < panelCount; p++ {
					m.active = p
					m.cursor[p] = min(15, max(0, m.rowCount(p)-1))
					m.editing = false
					view := assertFrame(t, m)
					if m.tooSmall() {
						if !strings.Contains(view, "Resize") {
							t.Fatal(view)
						}
						continue
					}
					if !strings.Contains(strings.Join(strings.Fields(view), " "), "q quit") {
						t.Fatalf("footer missing: %s", view)
					}
					if p != panelDiagnostics && !strings.Contains(view, "›") {
						t.Fatalf("selection missing: %s", view)
					}
					if p == panelProblems {
						m.expanded = true
						m.technical = true
						assertFrame(t, m)
						m.setExpanded(false)
					}
				}
			}
		}
	}
}

func TestDiagnosticsLastContentReachableAfterResize(t *testing.T) {
	m := geometryModel(t)
	m.active = panelDiagnostics
	m.technical = true
	for _, size := range [][2]int{{80, 24}, {40, 16}, {120, 40}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertFrame(t, m)
		m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		view := assertFrame(t, m)
		if !strings.Contains(strings.Join(strings.Fields(view), ""), "last-diagnostic-content") {
			t.Fatalf("last content unreachable: %s", view)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyHome})
		if m.bodyOffset != 0 {
			t.Fatal("home did not reset scroll")
		}
	}
}

func TestReviewerEditorGeometry(t *testing.T) {
	m := geometryModel(t)
	m.active = panelReviewer
	for _, name := range []string{"reviewer.model", "reviewer.provider"} {
		m.cursor[panelReviewer] = indexOfField(t, name)
		m.Update(key("enter"))
		for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 16}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := assertFrame(t, m)
			if !strings.Contains(view, "›") {
				t.Fatalf("editor selection missing: %s", view)
			}
		}
		m.Update(key("esc"))
	}
}

func TestExpandedProblemLastLineReachable(t *testing.T) {
	m := geometryModel(t)
	m.active = panelProblems
	m.expanded = true
	m.technical = true
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 16}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertFrame(t, m)
		m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		view := assertFrame(t, m)
		if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(view, "│", "")), ""), "steerfixretire") {
			t.Fatalf("last detail line unreachable: %s", view)
		}
	}
}

func TestGuardDescriptionKeepsItsColumnWhenWrapped(t *testing.T) {
	for _, selected := range []bool{false, true} {
		row := ansi.Strip(guardTitle("example-guard", strings.Repeat("description ", 20), selected, 120))
		lines := strings.Split(strings.TrimRight(row, "\n"), "\n")
		if len(lines) < 2 {
			t.Fatal("fixture did not wrap")
		}
		column := lipgloss.Width(lines[0][:strings.Index(lines[0], "description")])
		for _, line := range lines[1:] {
			if lipgloss.Width(line[:strings.Index(line, "description")]) != column {
				t.Fatalf("description lost column %d: %q", column, line)
			}
		}
	}
}
