package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/steer-sh/steer-cli/internal/styles"
)

// header renders a panel title.
func header(s string) string {
	return styles.SectionHeader.Render(s) + "\n\n"
}

// section renders a sub-heading inside a panel.
func section(s string) string {
	return "\n" + styles.Bold.Render(s) + "\n"
}

// pointer marks the selected row.
func pointer(selected bool) string {
	if selected {
		return lipgloss.NewStyle().Foreground(styles.BrandPurple).Render("›")
	}
	return " "
}

func dim(s string) string     { return styles.MutedText.Render(s) }
func ok(s string) string      { return styles.SuccessText.Render(s) }
func warn(s string) string    { return styles.WarningText.Render(s) }
func errText(s string) string { return styles.ErrorText.Render(s) }

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
