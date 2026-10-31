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

// A person who guesses a key setting must be sent to the command that reads the key
// with no echo, not shown the list of settings.
func TestAKeySettingNamesTheKeyCommand(t *testing.T) {
	for _, name := range []string{"reviewer.api.key", "reviewer.api_key", "reviewer.key"} {
		_, err := settings.Find(name)
		if err == nil || !strings.Contains(err.Error(), "steer reviewer key set") {
			t.Errorf("Find(%q) = %v, want it to name steer reviewer key set", name, err)
		}
	}
}

// The variable setting holds a name. A key pasted into it would be written to a file
// every user can read.
func TestKeyEnvRefusesAValueThatIsNotAVariableName(t *testing.T) {
	f, err := settings.Find("reviewer.api.key_env")
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := f.Set(&c, "OPENROUTER_API_KEY"); err != nil || c.Reviewer.API.KeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("Set(OPENROUTER_API_KEY) = %v, stored %q", err, c.Reviewer.API.KeyEnv)
	}
	for _, v := range []string{"sk-or-v1-0123456789abcdef", "AIzaSyD0123456789abcdefGHIJKLmnopqrstu", strings.Repeat("A", 80)} {
		c := config.Config{}
		if err := f.Set(&c, v); err == nil || c.Reviewer.API.KeyEnv != "" {
			t.Errorf("Set(%q) stored %q, err %v; want refused", v, c.Reviewer.API.KeyEnv, err)
		}
	}
}

// A provider is chosen before its URL is written, so naming the API provider must
// not require the rest of its configuration.
func TestTheAPIProviderCanBeNamedBeforeItsURL(t *testing.T) {
	f, err := settings.Find("reviewer.provider")
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := f.Set(&c, "openai-compatible"); err != nil {
		t.Fatalf("Set(openai-compatible) = %v", err)
	}
	if err := f.Set(&c, "openai-compatibel"); err == nil {
		t.Error("Set accepted a misspelt provider")
	}
	url, _ := settings.Find("reviewer.api.base_url")
	if err := url.Set(&c, "http://api.example.com/v1"); err == nil {
		t.Error("base_url accepted plain http to a remote host")
	}
}

// TestToolsTakesOnlyAKnownMode: a typo in the tool mode is a write-time error, not a
// review that fails on every commit.
func TestToolsTakesOnlyAKnownMode(t *testing.T) {
	f, err := settings.Find("reviewer.api.tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"off", "auto", "required", ""} {
		var c config.Config
		if err := f.Set(&c, v); err != nil || c.Reviewer.API.Tools != v {
			t.Errorf("Set(%q) = %v, stored %q", v, err, c.Reviewer.API.Tools)
		}
	}
	var c config.Config
	if err := f.Set(&c, "on"); err == nil || c.Reviewer.API.Tools != "" {
		t.Errorf("Set(on) stored %q, err %v; want refused", c.Reviewer.API.Tools, err)
	}
}
