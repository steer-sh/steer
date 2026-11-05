package styles

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// The terminal renders the same stacked labels the dashboard does, so a value
// that runs past the terminal continues under its own label rather than falling
// back to column zero. Each surface keeps its own column widths.

// minWrapWidth keeps wrapping valid on a terminal too narrow for the indent the
// caller asked for.
const minWrapWidth = 1

// maxLineWidth caps a wrapped line. Long prose is harder to read the wider it
// runs, and the dashboard caps its own body for the same reason.
const maxLineWidth = 100

// defaultWidth is what a pipe or a closed terminal reads as.
const defaultWidth = 80

// Width is the writable width of this terminal, capped for readability. A
// redirected stdout reports no size, so it reads as the default.
func Width() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		w = defaultWidth
	}
	if w > maxLineWidth {
		w = maxLineWidth
	}
	return w
}

// Wrap breaks text to the cells left of width after indent, and puts indent in
// front of every line it produces. ANSI styles and wide runes count as the cells
// they occupy. The result ends in a newline.
func Wrap(text, indent string, width int) string {
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(indent) >= width {
		indent = strings.Repeat(" ", max(0, width-1))
	}
	avail := width - lipgloss.Width(indent)
	if avail < minWrapWidth {
		avail = minWrapWidth
	}
	var b strings.Builder
	for _, line := range strings.Split(ansi.Wrap(text, avail, ""), "\n") {
		b.WriteString(indent + line + "\n")
	}
	return b.String()
}

// Field renders one labeled line: the label in its own column, the value wrapped
// under a hanging indent so a second line lines up with the first. Extra values
// stack under the same hanging indent, so a field may carry several sentences
// without a second label.
func Field(label string, values []string, indent string, labelWidth, width int) string {
	lead := indent + strings.Repeat(" ", labelWidth+1)
	out := ""
	for i, value := range values {
		lines := strings.Split(strings.TrimRight(Wrap(value, lead+hang(value), width), "\n"), "\n")
		if i == 0 {
			head := indent + FieldLabel.Render(fmt.Sprintf("%-*s", labelWidth, label)) + " "
			out += head + strings.TrimPrefix(lines[0], lead+hang(value)) + "\n"
			lines = lines[1:]
		} else {
			lines[0] = lead + strings.TrimPrefix(lines[0], lead+hang(value))
		}
		for _, line := range lines {
			out += line + "\n"
		}
	}
	return out
}

// bulletMark leads a value that is one of several under a label. Such a value
// hangs under its own text, so a wrapped bullet cannot be read as a new one.
const bulletMark = "· "

// hang is the extra indent a value's continuation lines carry. A value arrives
// styled, so the mark is read off the text rather than off the escape sequence
// in front of it.
func hang(value string) string {
	if strings.HasPrefix(ansi.Strip(value), bulletMark) {
		return strings.Repeat(" ", lipgloss.Width(bulletMark))
	}
	return ""
}
