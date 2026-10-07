package cmd

import (
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/config"
)

// tempSettingsHome points the settings file at a temporary home, so a test never reads
// or rewrites the operator's own configuration.
func tempSettingsHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
}

func setSetting(t *testing.T, key, value string) {
	t.Helper()
	if err := configSetCmd.RunE(configSetCmd, []string{key, value}); err != nil {
		t.Fatalf("set %s=%s: %v", key, value, err)
	}
}

func TestConfigSetWritesEveryReviewerKey(t *testing.T) {
	tempSettingsHome(t)
	setSetting(t, "reviewer.provider", "codex")
	setSetting(t, "reviewer.model", "gpt-reviewer")
	setSetting(t, "reviewer.effort", "high")
	setSetting(t, "reviewer.runs", "5")
	setSetting(t, "reviewer.timeout", "300")

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{Reviewer: config.Reviewer{
		Provider: "codex", Model: "gpt-reviewer", Effort: "high", Runs: 5, Timeout: 300,
	}}
	if got != want {
		t.Fatalf("stored = %#v; want %#v", got, want)
	}
}

// An empty value clears a key rather than storing an empty string, so the setting
// returns to the shipped default instead of becoming an override that names nothing.
func TestConfigSetClearsAKeyOnAnEmptyValue(t *testing.T) {
	tempSettingsHome(t)
	setSetting(t, "reviewer.effort", "high")
	setSetting(t, "reviewer.runs", "5")
	setSetting(t, "reviewer.effort", "")
	setSetting(t, "reviewer.runs", "")

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != (config.Config{}) {
		t.Fatalf("cleared config = %#v; want no overrides", got)
	}
}

func TestConfigSetRefusesValuesTheGateCouldNotRun(t *testing.T) {
	tempSettingsHome(t)
	cases := []struct{ key, value string }{
		{"reviewer.nonsense", "x"},
		{"reviewer.provider", "gpt"},
		{"reviewer.runs", "many"},
		{"reviewer.timeout", "-1"},
	}
	for _, c := range cases {
		if err := configSetCmd.RunE(configSetCmd, []string{c.key, c.value}); err == nil {
			t.Errorf("set %s=%s succeeded; want a write-time refusal", c.key, c.value)
		}
	}
	if got, err := config.Load(); err != nil || got != (config.Config{}) {
		t.Fatalf("a refused write left %#v (err %v); want an untouched file", got, err)
	}
}

// captureStdout runs fn with stdout redirected, so a test can read what an operator
// would see.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = saved
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(out)
}

// A timeout is not part of the reviewer's identity, so set names the harness wall
// clock it does not reach.
func TestConfigSetTimeoutNamesTheRegistrationItDoesNotReach(t *testing.T) {
	tempSettingsHome(t)
	out := captureStdout(t, func() { setSetting(t, "reviewer.timeout", "300") })
	if !strings.Contains(out, "steer commit-gate install") {
		t.Errorf("set did not name the re-registration:\n%s", out)
	}
}

func writeLine(t *testing.T, path string, record map[string]any) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
