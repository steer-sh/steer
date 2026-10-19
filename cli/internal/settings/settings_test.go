package settings_test

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/settings"
)

// checkSource compares one rendered source label.
func checkSource(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", label, got, want)
	}
}

// The environment outranks the file, so a value written here is not the value in
// force. The source column is what says so.
func TestFieldSourceNamesTheLayerInForce(t *testing.T) {
	field, err := settings.Find("reviewer.effort")
	if err != nil {
		t.Fatalf("settings.Find: %v", err)
	}
	stored := config.Config{Reviewer: config.Reviewer{Effort: "high"}}

	t.Setenv("COMMIT_GATE_EFFORT", "")
	checkSource(t, "an unset key is the default", settings.Source(field, config.Config{}), "default")
	checkSource(t, "a stored key is the file's", settings.Source(field, stored), "config")

	t.Setenv("COMMIT_GATE_EFFORT", "low")
	checkSource(t, "the environment outranks the file", settings.Source(field, stored), "env COMMIT_GATE_EFFORT")
}

// A numeric override counts only where it parses above zero, the same test
// gate.WithEnv applies; otherwise the file's value is still the one in force.
func TestFieldSourceIgnoresAnUnusableNumericOverride(t *testing.T) {
	field, err := settings.Find("reviewer.runs")
	if err != nil {
		t.Fatalf("settings.Find: %v", err)
	}
	stored := config.Config{Reviewer: config.Reviewer{Runs: 5}}
	t.Setenv("COMMIT_GATE_RUNS", "0")
	checkSource(t, "a zero override is no override", settings.Source(field, stored), "config")
}

func TestConfigHelpNamesEverySetting(t *testing.T) {
	help := settings.KeyHelp()
	for _, name := range settings.Names() {
		if !strings.Contains(help, name) {
			t.Errorf("the generated help omits %s", name)
		}
	}
}
