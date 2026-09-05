package cmd

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

func TestStatusLineWrapTarget(t *testing.T) {
	steerWrapping := "steer statusline " + claude.EncodeWrapped("~/.claude/statusline.sh")

	cases := []struct {
		name     string
		existing string
		mine     bool
		wrap     bool
		replace  bool
		want     string
		wantOK   bool
	}{
		{
			name: "an empty slot wraps nothing",
			wrap: true,
		},
		{
			name:     "--wrap takes over a foreign command",
			existing: "~/.claude/statusline.sh",
			wrap:     true,
			want:     "~/.claude/statusline.sh",
			wantOK:   true,
		},
		{
			name:     "re-installing over steer's own entry keeps what it wraps",
			existing: steerWrapping,
			mine:     true,
			want:     "~/.claude/statusline.sh",
			wantOK:   true,
		},
		{
			// --replace says discard, and a wrapped command is what there is to
			// discard: keeping it would make the flag inert on the one entry that
			// carries anything.
			name:     "--replace discards the command steer's own entry wraps",
			existing: steerWrapping,
			mine:     true,
			replace:  true,
		},
		{
			name:     "--replace discards a foreign command",
			existing: "~/.claude/statusline.sh",
			replace:  true,
		},
		{
			name:     "steer's own unwrapped entry wraps nothing",
			existing: "steer statusline",
			mine:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := statusLineWrapTarget(tc.existing, tc.mine, tc.wrap, tc.replace)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("statusLineWrapTarget() = (%q, %v), want (%q, %v)",
					got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
