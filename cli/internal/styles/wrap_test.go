package styles

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// column is the cell a substring sits in, so a styled line is measured as a
// reader sees it rather than by its bytes.
func column(line, text string) int {
	plain := ansi.Strip(line)
	i := strings.Index(plain, text)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(plain[:i])
}

const long = "a long sentence that has to wrap more than once, and then once more."

// A value that carries a bullet hangs under its own text, so a wrapped bullet
// cannot be read as a new one. The mark arrives styled, and the escape sequence
// in front of it must not hide it.
func TestABulletHangsUnderItsOwnText(t *testing.T) {
	cases := []struct{ name, value string }{
		{"plain", "· " + long},
		{"styled", "\x1b[90m· " + long + "\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Field("Why", []string{"the reason", tc.value}, "  ", 6, 50)
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			mark := -1
			for i, line := range lines {
				if strings.Contains(line, "· ") {
					mark = i
					break
				}
			}
			if mark < 0 || mark == len(lines)-1 {
				t.Fatalf("the bullet never wrapped, so the hang is untested:\n%s", out)
			}
			bullet := column(lines[mark], "·")
			cont := column(lines[mark+1], "than once")
			if cont != bullet+2 {
				t.Errorf("continuation starts in column %d, want %d:\n%s", cont, bullet+2, out)
			}
		})
	}
}

// The label column holds the label, whatever the first value carries. A bullet
// there is text like any other and never pushes the first line right.
func TestTheFirstValueStartsInTheValueColumn(t *testing.T) {
	for _, value := range []string{long, "· " + long} {
		out := Field("Why", []string{value}, "  ", 6, 50)
		first := strings.Split(out, "\n")[0]
		if got := column(first, value[:6]); got != 2+6+1 {
			t.Errorf("value starts in column %d, want %d: %q", got, 2+6+1, first)
		}
	}
}
