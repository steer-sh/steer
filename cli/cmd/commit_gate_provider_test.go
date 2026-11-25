package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// A --provider that differs from the configured one refuses the configured model,
// which names a model of the other provider, and drops the configured effort.
func TestProviderSwitchRefusesTheOtherProvidersModel(t *testing.T) {
	cfg := gate.Defaults()
	cfg.ReviewerProvider, cfg.ReviewerModel, cfg.ReviewerModelOverride, cfg.ReviewerEffort =
		"claude-code", "claude-sonnet-5-5", "claude-sonnet-5-5", "low"

	if _, err := providerSwitch(cfg, "codex", "", ""); err == nil || !strings.Contains(err.Error(), "--model <model>") {
		t.Fatalf("a switch to codex kept the claude-code model: err = %v", err)
	}
	got, err := providerSwitch(cfg, "codex", "gpt-5.6-luna", "")
	if err != nil || got.ReviewerEffort != "" {
		t.Fatalf("a switch with --model kept effort %q (err %v), want the provider's own", got.ReviewerEffort, err)
	}
	if got, err := providerSwitch(cfg, "codex", "gpt-5.6-luna", "high"); err != nil || got.ReviewerEffort != "high" {
		t.Fatalf("an explicit --effort was dropped: %q (err %v)", got.ReviewerEffort, err)
	}
	if got, err := providerSwitch(cfg, "claude", "", ""); err != nil || got.ReviewerEffort != "low" {
		t.Fatalf("an alias of the configured provider was treated as a switch: %+v (err %v)", got, err)
	}
	unset := cfg
	unset.ReviewerProvider = ""
	if _, err := providerSwitch(unset, "codex", "", ""); err == nil {
		t.Fatal("an unset provider reads as agy, yet a switch to codex kept its model")
	}
	cfg.ReviewerModelOverride = ""
	if _, err := providerSwitch(cfg, "codex", "", ""); err != nil {
		t.Fatalf("no configured model, yet the switch was refused: %v", err)
	}
}
