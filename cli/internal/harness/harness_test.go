package harness

import (
	"path/filepath"
	"runtime"
	"testing"
)

// fakeHome points os.UserHomeDir at a test directory. It sets both variables
// because Go reads USERPROFILE on Windows and HOME everywhere else.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	return dir
}

func TestParseAcceptsAliasAndRejectsUnknown(t *testing.T) {
	for name, want := range map[string]ID{
		"claude":      ClaudeCode,
		"claude-code": ClaudeCode,
		"  Codex ":    Codex,
		"pi":          Pi,
	} {
		got, err := Parse(name)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q, nil", name, got, err, want)
		}
	}
	if _, err := Parse("cursor"); err == nil {
		t.Error("Parse(\"cursor\") returned no error")
	}
}

func TestDetectReadsTheSessionMarker(t *testing.T) {
	t.Setenv("STEER_HARNESS", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("PI_CODING_AGENT", "")
	if got := Detect(); got != Unknown {
		t.Errorf("Detect with no marker = %q; want Unknown", got)
	}
	t.Setenv("PI_CODING_AGENT", "true")
	if got := Detect(); got != Pi {
		t.Errorf("Detect with PI_CODING_AGENT = %q; want %q", got, Pi)
	}
	t.Setenv("CLAUDECODE", "1")
	if got := Detect(); got != Unknown {
		t.Errorf("Pi inside Claude Code detected as %q; a nested session must be Unknown", got)
	}
	t.Setenv("CLAUDECODE", "")
	t.Setenv("PI_CODING_AGENT", "")
	t.Setenv("CODEX_SESSION_ID", "01a055bc-8abe-7d31-8a36-70a36665e791")
	if got := Detect(); got != Codex {
		t.Errorf("Detect with CODEX_SESSION_ID = %q; want %q", got, Codex)
	}
	t.Setenv("CLAUDECODE", "1")
	if got := Detect(); got != Unknown {
		t.Errorf("two markers detected as %q; a nested session must be Unknown", got)
	}
	t.Setenv("CODEX_SESSION_ID", "")
	if got := Detect(); got != ClaudeCode {
		t.Errorf("Detect with CLAUDECODE = %q; want %q", got, ClaudeCode)
	}
	t.Setenv("CODEX_SESSION_ID", "01a055bc-8abe-7d31-8a36-70a36665e791")
	t.Setenv("STEER_HARNESS", "codex")
	if got := Detect(); got != Codex {
		t.Errorf("STEER_HARNESS did not override the session marker: got %q", got)
	}
}

func TestParseOrDetectFallsBackToDetection(t *testing.T) {
	t.Setenv("STEER_HARNESS", "codex")
	got, err := ParseOrDetect("")
	if err != nil || got != Codex {
		t.Fatalf("ParseOrDetect(\"\") = %q, %v; want %q, nil", got, err, Codex)
	}
	if got, err := ParseOrDetect("claude"); err != nil || got != ClaudeCode {
		t.Errorf("an explicit name lost to detection: got %q, %v", got, err)
	}
}

func TestConfigDirHonorsScopeAndHomeOverride(t *testing.T) {
	codex, _ := Get(Codex)
	t.Setenv("CODEX_HOME", "/elsewhere/codex")
	got, err := codex.ConfigDir(GlobalScope, "/repo")
	if err != nil || got != "/elsewhere/codex" {
		t.Errorf("global Codex dir = %q, %v; want the CODEX_HOME override", got, err)
	}
	got, err = codex.ConfigDir(ProjectScope, "/repo")
	if err != nil || got != filepath.Join("/repo", ".codex") {
		t.Errorf("project Codex dir = %q, %v", got, err)
	}
	if _, err := codex.ConfigDir("user", "/repo"); err == nil {
		t.Error("an invalid scope returned no error")
	}

	claude, _ := Get(ClaudeCode)
	home := fakeHome(t)
	got, err = claude.ConfigDir(GlobalScope, "/repo")
	if err != nil || got != filepath.Join(home, ".claude") {
		t.Errorf("global Claude dir = %q, %v", got, err)
	}

	// Pi is the one entry whose global directory is not its project one: the
	// config lives under ~/.pi/agent while a repository carries .pi.
	pi, _ := Get(Pi)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	got, err = pi.ConfigDir(GlobalScope, "/repo")
	if err != nil || got != filepath.Join(home, ".pi", "agent") {
		t.Errorf("global Pi dir = %q, %v; want ~/.pi/agent", got, err)
	}
	got, err = pi.ConfigDir(ProjectScope, "/repo")
	if err != nil || got != filepath.Join("/repo", ".pi") {
		t.Errorf("project Pi dir = %q, %v", got, err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", "/elsewhere/pi")
	got, err = pi.ConfigDir(GlobalScope, "/repo")
	if err != nil || got != "/elsewhere/pi" {
		t.Errorf("global Pi dir = %q, %v; want the PI_CODING_AGENT_DIR override", got, err)
	}
}

func TestEveryRegistryEntryIsComplete(t *testing.T) {
	all := All()
	if len(all) != len(registry) {
		t.Fatalf("All returned %d entries; the registry holds %d", len(all), len(registry))
	}
	for _, h := range all {
		if h.ID == Unknown || h.DisplayName == "" || h.InstructionFile == "" ||
			h.ConfigDirName == "" || h.DetectEnv == "" {
			t.Errorf("%+v is missing a required field", h)
		}
	}
}

// The denials corpus records what a harness's own classifier blocked, so a
// harness with no such event must not be reported as covered by it.
func TestOnlyClaudeCodeReportsPermissionDenied(t *testing.T) {
	codex, _ := Get(Codex)
	if codex.Capabilities.PermissionDenied {
		t.Error("Codex claims a PermissionDenied event")
	}
	claude, _ := Get(ClaudeCode)
	if !claude.Capabilities.PermissionDenied {
		t.Error("Claude Code lost its PermissionDenied capability")
	}
	pi, _ := Get(Pi)
	if pi.Capabilities.PermissionDenied {
		t.Error("Pi claims a PermissionDenied event")
	}
	if !codex.Capabilities.TrustReview || !pi.Capabilities.TrustReview ||
		claude.Capabilities.TrustReview {
		t.Error("trust review is the posture of every harness but Claude Code")
	}
}
