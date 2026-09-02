package cmd

import (
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
