package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	focus             = lipgloss.AdaptiveColor{Dark: "#A78BFA", Light: "#6941C6"}
	secondaryColor    = lipgloss.AdaptiveColor{Dark: "#B0B6C4", Light: "#535C6C"}
	surfaceColor      = lipgloss.AdaptiveColor{Dark: "#2E3340", Light: "#EDF0F5"}
	surfaceText       = lipgloss.AdaptiveColor{Dark: "#E4E7EE", Light: "#242B36"}
	edgeColor         = lipgloss.AdaptiveColor{Dark: "#555E70", Light: "#BDC4CE"}
	headingStyle      = lipgloss.NewStyle().Bold(true)
	secondaryStyle    = lipgloss.NewStyle().Foreground(secondaryColor)
	metadataStyle     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#969EAE", Light: "#626C7C"})
	positiveStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Dark: "#6FCF97", Light: "#237A49"})
	cautionStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Dark: "#E8B86D", Light: "#8A5700"})
	adverseStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Dark: "#F08A8A", Light: "#B4233D"})
	activeTabStyle    = lipgloss.NewStyle().Bold(true).Background(focus).Foreground(lipgloss.AdaptiveColor{Dark: "#211A32", Light: "#FFFFFF"})
	selectedStyle     = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Dark: "#393249", Light: "#EDE7F6"}).Foreground(surfaceText)
	selectedNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Dark: "#C4B5FD", Light: "#6941C6"})
	surfaceStyle      = lipgloss.NewStyle().Background(surfaceColor).Foreground(surfaceText)
)

func secondary(s string) string { return secondaryStyle.Render(s) }

func (m *Model) gutter() int {
	if m.width > 0 && m.width < 60 {
		return 1
	}
	return 2
}

// selectedRow keeps status spans independent of the focused name.
func selectedRow(name, suffix string, selected bool, width int) string {
	if selected {
		name = selectedNameStyle.Render(name)
	}
	text := name + suffix
	lines := strings.Split(strings.TrimRight(wrapTo(text, "  ", width), "\n"), "\n")
	lines[0] = pointer(selected) + " " + strings.TrimPrefix(lines[0], "  ")
	for i, line := range lines {
		if selected {
			lines[i] = paintSurface(selectedStyle.Width(width), line)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func inset(body string, width int, editing bool) string {
	edge := edgeColor
	if editing {
		edge = focus
	}
	return paintSurface(surfaceStyle.Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(edge).BorderBackground(surfaceColor).Padding(0, 1).
		Width(max(1, width-1)), strings.TrimRight(body, "\n")) + "\n"
}

// paintSurface restores the surface after nested foreground styles reset SGR.
func paintSurface(style lipgloss.Style, text string) string {
	base := lipgloss.NewStyle().Foreground(style.GetForeground()).Background(style.GetBackground()).Render("x")
	prefix := strings.SplitN(base, "x", 2)[0]
	rendered := style.Render(text)
	if prefix == "" {
		return rendered
	}
	rendered = strings.ReplaceAll(rendered, "\x1b[0m", "\x1b[0m"+prefix)
	rendered = strings.ReplaceAll(rendered, "\x1b[m", "\x1b[m"+prefix)
	return prefix + strings.ReplaceAll(rendered, "\n", "\x1b[0m\n"+prefix) + "\x1b[0m"
}
