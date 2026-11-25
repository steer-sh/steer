//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stampedSteer builds the CLI into dir with the release stamps a published build carries.
func stampedSteer(t *testing.T, dir, version, commit string) string {
	t.Helper()
	path := filepath.Join(dir, exe("steer"))
	ldflags := fmt.Sprintf("-X 'github.com/steer-sh/steer-cli/internal/version.Version=%s' "+
		"-X 'github.com/steer-sh/steer-cli/internal/version.GitCommit=%s'", version, commit)
	if out, err := exec.Command("go", "build", "-ldflags", ldflags, "-o", path, "..").CombinedOutput(); err != nil {
		t.Fatalf("build steer %s: %v\n%s", version, err, out)
	}
	return path
}

// TestSessionStartUpdatesTheHooksFromANewerPluginCopy places release 0.1.7 at the
// stable executable through a global check runner, then runs the SessionStart
// command from a Claude Code plugin copy of 0.1.8, and requires the startup line and
// the stable executable to name 0.1.8.
//
// pins existing behaviour
func TestSessionStartUpdatesTheHooksFromANewerPluginCopy(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	old := stampedSteer(t, s.path("old"), "0.1.7", "aaaaaaa")
	if r := s.Run(s.Repo, "", nil, old, "check", "install", "--harness", "claude-code", "--scope", "global"); r.Code != 0 {
		t.Fatalf("check install exited %d\n%s", r.Code, r.Output())
	}
	stable := filepath.Join(s.Home, ".steer", "bin", exe("steer"))
	if r := s.Run(s.Repo, "", nil, stable, "--version"); !strings.HasPrefix(r.Stdout, "steer 0.1.7 ") {
		t.Fatalf("stable executable before the update: %q", r.Stdout)
	}

	copyDir := filepath.Join(s.Home, ".claude", "plugins", "cache", "m", "steer", "0.1.8",
		"bin", runtime.GOOS+"-"+runtime.GOARCH)
	plugin := stampedSteer(t, copyDir, "0.1.8", "bbbbbbb")
	r := s.Run(s.Repo, "", nil, plugin, "report", "cadence")
	s.WaitForRefresh()
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("report cadence exited %d with stderr %q", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "steer: hooks updated 0.1.7 → 0.1.8") {
		t.Errorf("startup output does not report the update:\n%s", r.Stdout)
	}
	if got := s.Run(s.Repo, "", nil, stable, "--version"); !strings.HasPrefix(got.Stdout, "steer 0.1.8 ") {
		t.Errorf("stable executable after the update: %q", got.Stdout)
	}
}
