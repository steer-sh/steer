package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
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

// checkSource compares one rendered source label.
func checkSource(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", label, got, want)
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

// The environment outranks the file, so a value written here is not the value in
// force. The source column is what says so.
func TestFieldSourceNamesTheLayerInForce(t *testing.T) {
	field, err := findConfigField("reviewer.effort")
	if err != nil {
		t.Fatalf("findConfigField: %v", err)
	}
	stored := config.Config{Reviewer: config.Reviewer{Effort: "high"}}

	t.Setenv("COMMIT_GATE_EFFORT", "")
	checkSource(t, "an unset key is the default", fieldSource(field, config.Config{}), "default")
	checkSource(t, "a stored key is the file's", fieldSource(field, stored), "config")

	t.Setenv("COMMIT_GATE_EFFORT", "low")
	checkSource(t, "the environment outranks the file", fieldSource(field, stored), "env COMMIT_GATE_EFFORT")
}

// A numeric override counts only where it parses above zero, the same test
// gate.WithEnv applies; otherwise the file's value is still the one in force.
func TestFieldSourceIgnoresAnUnusableNumericOverride(t *testing.T) {
	field, err := findConfigField("reviewer.runs")
	if err != nil {
		t.Fatalf("findConfigField: %v", err)
	}
	stored := config.Config{Reviewer: config.Reviewer{Runs: 5}}
	t.Setenv("COMMIT_GATE_RUNS", "0")
	checkSource(t, "a zero override is no override", fieldSource(field, stored), "config")
}

func TestConfigHelpNamesEverySetting(t *testing.T) {
	help := configKeyHelp()
	for _, name := range configFieldNames() {
		if !strings.Contains(help, name) {
			t.Errorf("the generated help omits %s", name)
		}
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

// A reviewer change is measured across two instruments by every class still
// collecting, so set names those classes before it writes.
func TestConfigSetNamesTheMeasurementsAReviewerChangeLandsIn(t *testing.T) {
	tempSettingsHome(t)
	state := t.TempDir()
	t.Setenv("COMMIT_GATE_STATE_DIR", state)
	authored := gate.Defaults().InstrumentID()
	writeLine(t, filepath.Join(state, "interventions.jsonl"), map[string]any{
		"id": "bare-except-1", "class": "bare-except", "match": "boom", "tier": "check",
		"status": "active", "installed_ts": 4000, "instrument": authored,
		"scope": map[string]any{"version": mine.ScopeVersion, "all": true},
	})
	writeLine(t, filepath.Join(state, "adjudication.test.jsonl"), map[string]any{
		"ts": 1000, "phase": "deny", "firing": 1, "files": 1, "instrument": authored,
	})

	out := captureStdout(t, func() { setSetting(t, "reviewer.effort", "high") })
	if !strings.Contains(out, "bare-except") {
		t.Errorf("set did not name the class it lands in:\n%s", out)
	}
	if !strings.Contains(out, "minor") {
		t.Errorf("an effort change is a minor drift; got:\n%s", out)
	}
}

// A timeout is not part of the reviewer's identity, so it drifts no measurement and
// says so about the harness wall clock instead.
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
