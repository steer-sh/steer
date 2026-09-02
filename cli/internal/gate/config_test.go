package gate

import (
	"runtime"
	"testing"
	"time"

	steerconfig "github.com/steer-sh/steer-cli/internal/config"
)

func tempConfigHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	for _, key := range []string{
		"COMMIT_GATE_STATE_DIR", "COMMIT_REVIEW_STATE", "COMMIT_GATE_RUNS",
		"COMMIT_GATE_TIMEOUT", "COMMIT_GATE_MAX_FIRINGS", "COMMIT_GATE_MODEL",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadAppliesFileBetweenDefaultsAndEnvironment(t *testing.T) {
	tempConfigHome(t)
	if err := steerconfig.Save(steerconfig.Config{Reviewer: steerconfig.Reviewer{
		Provider: "codex",
		Model:    "file-model",
		Runs:     5,
		Timeout:  210,
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("COMMIT_GATE_MODEL", "env-model")
	t.Setenv("COMMIT_GATE_RUNS", "7")
	t.Setenv("COMMIT_GATE_TIMEOUT", "240")

	cfg := Load()
	if cfg.ReviewerProvider != "codex" {
		t.Errorf("ReviewerProvider = %q; want codex", cfg.ReviewerProvider)
	}
	if cfg.ReviewerModelOverride != "env-model" {
		t.Errorf("ReviewerModelOverride = %q; want env-model", cfg.ReviewerModelOverride)
	}
	if cfg.ReviewerRuns != 7 {
		t.Errorf("ReviewerRuns = %d; want 7", cfg.ReviewerRuns)
	}
	if cfg.ReviewTimeout != 240*time.Second {
		t.Errorf("ReviewTimeout = %s; want 4m0s", cfg.ReviewTimeout)
	}
}

func TestLoadMissingFileKeepsShippedDefaults(t *testing.T) {
	tempConfigHome(t)
	cfg := Load()
	if cfg.ReviewerProvider != "agy" || cfg.ReviewerModel != defaultReviewerModel {
		t.Fatalf("reviewer defaults = %q/%q; want agy/%s",
			cfg.ReviewerProvider, cfg.ReviewerModel, defaultReviewerModel)
	}
	if cfg.ReviewerRuns != defaultReviewerRuns || cfg.ReviewTimeout != defaultReviewTimeout {
		t.Fatalf("panel defaults = %d/%s; want %d/%s",
			cfg.ReviewerRuns, cfg.ReviewTimeout, defaultReviewerRuns, defaultReviewTimeout)
	}
}
