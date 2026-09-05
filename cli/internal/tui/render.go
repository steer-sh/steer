package tui

import (
	"fmt"
	"strings"

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

// stickyLines is the panel header and the blank line under it, which the window
// holds still.
const stickyLines = 2

// contextAbove is how many lines the window keeps above the selected row.
const contextAbove = 1

// windowBody trims a panel body to budget lines, keeping the selected row in view.
// A budget of zero returns the body whole.
func windowBody(body string, budget int) string {
	if budget <= 0 {
		return body
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) <= budget {
		return body
	}
	sticky := stickyLines
	if len(lines) <= sticky || budget <= sticky+1 {
		sticky = 0
	}
	rest := lines[sticky:]
	// One line goes to the indicator that says what is hidden.
	view := budget - sticky - 1
	if view < 1 {
		view = 1
	}
	if view >= len(rest) {
		return body
	}
	// The window opens at the selected row, one line of context above it. A row is
	// the first line of its entry, and a Classes entry runs several lines, so
	// putting the selection at the bottom would hide everything it says.
	start := 0
	if a := markerLine(rest); a > contextAbove {
		start = a - contextAbove
	}
	if max := len(rest) - view; start > max {
		start = max
	}
	out := append([]string{}, lines[:sticky]...)
	out = append(out, rest[start:start+view]...)
	out = append(out, hiddenNote(start, len(rest)-start-view))
	windowed := strings.Join(out, "\n")
	// Keep the body's own ending. A panel that ends without a newline gains a
	// blank line under the chrome's separator if the window adds one.
	if strings.HasSuffix(body, "\n") {
		windowed += "\n"
	}
	return windowed
}

// markerLine is the index of the selected row, or -1 when the panel selects none.
func markerLine(lines []string) int {
	marker := pointer(true)
	for i, line := range lines {
		if strings.Contains(line, marker) {
			return i
		}
	}
	return -1
}

// hiddenNote names the rows the window cut, on each side that has any.
func hiddenNote(above, below int) string {
	switch {
	case above > 0 && below > 0:
		return dim(fmt.Sprintf("  ↑ %d more above · ↓ %d more below", above, below))
	case above > 0:
		return dim(fmt.Sprintf("  ↑ %d more above", above))
	case below > 0:
		return dim(fmt.Sprintf("  ↓ %d more below", below))
	}
	return ""
}
