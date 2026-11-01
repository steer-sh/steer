package cmd

import (
	"os"
	"path/filepath"
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

// An occupied slot is refused with a failing exit, so a script that installs the
// row can tell that nothing was written; the operator's entry stays as it was.
func TestStatusLineInstallRefusesAnOccupiedSlot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	settings := filepath.Join(home, claude.ClaudeDirName, claude.SettingsName)
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	const foreign = `{"statusLine": {"type": "command", "command": "~/.claude/statusline.sh"}}`
	if err := os.WriteFile(settings, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	old := statusLineScope
	statusLineScope = claude.GlobalScope
	t.Cleanup(func() { statusLineScope = old })

	if err := statusLineInstallCmd.RunE(statusLineInstallCmd, nil); err == nil {
		t.Fatal("install over a foreign status line returned no error")
	}
	if raw, _ := os.ReadFile(settings); string(raw) != foreign {
		t.Fatalf("install changed the settings file: %s", raw)
	}
}
