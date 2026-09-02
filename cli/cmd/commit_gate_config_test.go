package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func TestBuildReviewerDoesNotHandAgyPinToConfiguredCodex(t *testing.T) {
	oldProvider, oldModel := commitGateProvider, commitGateModel
	commitGateProvider, commitGateModel = "", ""
	t.Cleanup(func() {
		commitGateProvider, commitGateModel = oldProvider, oldModel
	})

	cfg := gate.Defaults()
	cfg.ReviewerProvider = "codex"
	reviewer, err := buildReviewer(cfg)
	if err != nil {
		t.Fatalf("buildReviewer: %v", err)
	}
	if reviewer.Name() != "codex" || reviewer.ModelLabel() != "codex-default" {
		t.Fatalf("reviewer = %s/%s; want codex/codex-default",
			reviewer.Name(), reviewer.ModelLabel())
	}
}

func TestBuildReviewerProviderFlagOverridesConfiguredProvider(t *testing.T) {
	oldProvider, oldModel := commitGateProvider, commitGateModel
	commitGateProvider, commitGateModel = "claude-code", ""
	t.Cleanup(func() {
		commitGateProvider, commitGateModel = oldProvider, oldModel
	})

	cfg := gate.Defaults()
	cfg.ReviewerProvider = "codex"
	cfg.ReviewerModelOverride = "configured-model"
	reviewer, err := buildReviewer(cfg)
	if err != nil {
		t.Fatalf("buildReviewer: %v", err)
	}
	if reviewer.Name() != "claude-code" || reviewer.ModelLabel() != "configured-model" {
		t.Fatalf("reviewer = %s/%s; want claude-code/configured-model",
			reviewer.Name(), reviewer.ModelLabel())
	}
}

func TestResolvedGateConfigNamesTheModelThatWouldRun(t *testing.T) {
	oldProvider, oldModel := commitGateProvider, commitGateModel
	commitGateProvider, commitGateModel = "codex", ""
	t.Cleanup(func() {
		commitGateProvider, commitGateModel = oldProvider, oldModel
	})
	// gateConfig reads the operator's real settings file otherwise. USERPROFILE is
	// what os.UserHomeDir reads on Windows.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COMMIT_GATE_MODEL", "")

	// A status view reads ReviewerModel. Without resolution it holds the shipped agy
	// pin, so every render target names a Gemini model under a Codex reviewer.
	if pin := gateConfig().ReviewerModel; pin != "gemini-3.7-flash-high" {
		t.Fatalf("unresolved ReviewerModel = %q; want the shipped pin", pin)
	}
	resolved, _, err := resolvedGateConfig()
	if err != nil {
		t.Fatalf("resolvedGateConfig: %v", err)
	}
	if got := resolved.ReviewerModel; got != "codex-default" {
		t.Fatalf("resolved ReviewerModel = %q; want codex-default", got)
	}
}

func TestResolvedGateConfigStampsTheConfiguredModelUnderAnUnknownProvider(t *testing.T) {
	oldProvider, oldModel := commitGateProvider, commitGateModel
	commitGateProvider, commitGateModel = "", ""
	t.Cleanup(func() {
		commitGateProvider, commitGateModel = oldProvider, oldModel
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COMMIT_GATE_MODEL", "")

	// The settings file is the path under test. COMMIT_GATE_MODEL would write
	// ReviewerModel itself, so a test driven by it passes without the fallback.
	if err := os.MkdirAll(filepath.Join(home, ".steer"), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	settings := `{"reviewer":{"provider":"nonesuch","model":"operator-model"}}`
	if err := os.WriteFile(filepath.Join(home, ".steer", "config.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, reviewer, err := resolvedGateConfig()
	if err == nil || reviewer != nil {
		t.Fatalf("resolvedGateConfig = %v, %v; want an error and no reviewer", reviewer, err)
	}
	// A snapshot renders this value. Steer's own pin under a provider the operator
	// never named would report a model that cannot run.
	if cfg.ReviewerModel != "operator-model" {
		t.Fatalf("ReviewerModel = %q; want the configured model", cfg.ReviewerModel)
	}
}
