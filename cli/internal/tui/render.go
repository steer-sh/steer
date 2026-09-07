package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/steer-sh/steer-cli/internal/styles"
)

// title renders a panel title wrapped to the terminal, so a narrow window folds
// the heading rather than running it past the edge.
func (m *Model) title(s string) string {
	parts := strings.SplitN(s, " — ", 2)
	text := headingStyle.Render(parts[0])
	if len(parts) == 2 {
		text += secondary(" — " + parts[1])
	}
	return wrapTo(text, "", m.bodyWidth()) + "\n"
}

// section renders a sub-heading inside a panel.
func section(s string) string {
	return "\n" + headingStyle.Render(s) + "\n"
}

// defaultWidth is the width a panel lays out at until the harness reports one.
// A terminal narrower than this wraps; a wider one uses what it has.
const defaultWidth = 80

// bodyWidth is the cells a panel body may draw into.
func (m *Model) bodyWidth() int {
	if m.width <= 0 {
		return defaultWidth
	}
	limit := 120
	if m.active == panelChannels || m.active == panelGuards {
		limit = 160
	}
	return min(limit, max(1, m.width-2*m.gutter()))
}

// wrapTo breaks text to the cells left of width after indent, and puts indent in
// front of every line it produces. ANSI styles and wide runes count as the
// terminal draws them, and a word longer than the column — a path or a regex —
// is broken rather than left to run off the screen, so nothing needs horizontal
// scrolling to read.
func wrapTo(text, indent string, width int) string {
	return styles.Wrap(text, indent, width)
}

// fieldLabelWidth is the column the stacked labels share. A label wider than it
// pushes its own line and the value still starts one space after.
const fieldLabelWidth = 9

// field renders one labeled line of a compact summary: the label in its own
// column, the value wrapped under a hanging indent so a second line lines up
// with the first. Secondary labels leave the values at primary contrast.
func field(label, value, indent string, width int) string {
	lead := indent + strings.Repeat(" ", fieldLabelWidth+1)
	lines := strings.Split(strings.TrimRight(wrapTo(value, lead, width), "\n"), "\n")
	head := indent + secondaryStyle.Render(fmt.Sprintf("%-*s", fieldLabelWidth, label)) + " "
	out := head + strings.TrimPrefix(lines[0], lead) + "\n"
	for _, line := range lines[1:] {
		out += line + "\n"
	}
	return out
}

// pointer marks the selected row.
func pointer(selected bool) string {
	if selected {
		return lipgloss.NewStyle().Foreground(focus).Render("›")
	}
	return " "
}

func dim(s string) string     { return metadataStyle.Render(s) }
func ok(s string) string      { return positiveStyle.Render(s) }
func warn(s string) string    { return cautionStyle.Render(s) }
func errText(s string) string { return adverseStyle.Render(s) }

// stickyLines is the panel header and the blank line under it, which the window
// holds still. A wrapped header runs to several lines, so the count is read off
// the body rather than assumed: the header ends at the first blank line.
func stickyLines(lines []string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			return i + 1
		}
		if i >= maxStickyLines {
			break
		}
	}
	return 0
}

// maxStickyLines caps what a header may hold still, so a body with no blank line
// never pins the whole window.
const maxStickyLines = 4

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
	sticky := stickyLines(lines)
	if len(lines) <= sticky || budget <= sticky+1 || budget < 10 {
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
	// the first line of its entry, and a Problems entry runs several lines, so
	// putting the selection at the bottom would hide everything it says.
	start := 0
	context := contextAbove
	if budget < 10 {
		context = 0
	}
	if a := markerLine(rest); a > context {
		start = a - context
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
	marker := "›"
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(ansi.Strip(line), " │"), marker+" ") {
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
