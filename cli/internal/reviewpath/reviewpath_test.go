package reviewpath

import "testing"

// TestMatch reads each pattern form against paths inside and outside it.
func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"cli/internal/tui/", "cli/internal/tui/model.go", true},
		{"cli/internal/tui/", "cli/internal/tui/sub/view.go", true},
		{"cli/internal/tui/", "cli/internal/tuix/model.go", false},
		{"cli/internal/tui/", "cli/internal/tui", false},
		{"*.go", "cli/cmd/root.go", true},
		{"*.go", "root.go", true},
		{"*.go", "cli/cmd/root.gox", false},
		{"*.go", ".go", false},
		{"cli/internal/tui/*.go", "cli/internal/tui/sub/view.go", true},
		{"cli/internal/tui/*.go", "cli/internal/tui/view.md", false},
		{"cli/internal/tui/*.go", "cli/cmd/root.go", false},
		{"cli/go.mod", "cli/go.mod", true},
		{"cli/go.mod", "cli/go.mod.bak", false},
		{"cli/go.mod", "x/cli/go.mod", false},
		{"cli/**", "cli/go.mod", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// TestValidate refuses each pattern outside the grammar and accepts each form inside it.
func TestValidate(t *testing.T) {
	for _, ok := range []string{"cli/internal/tui/", "*.go", "cli/tui/*.go", "cli/go.mod", "README.md"} {
		if err := Validate(ok); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", " ", "/abs/", `cli\tui\`, "cli/**", "**/*.go", "cli/*/x.go",
		"cli/*", "*", "*.", "a//b/", "../x/", "./x/", "x/..", "*.g*"} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", bad)
		}
	}
}
