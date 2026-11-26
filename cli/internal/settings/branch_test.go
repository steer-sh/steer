package settings_test

import (
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/settings"
)

// TestBranchConfigInheritsAndOverrides checks each layer of the branch reviewer's
// precedence: commit reviewer, file, environment, flags, and the drop of model and
// effort when the provider changes.
func TestBranchConfigInheritsAndOverrides(t *testing.T) {
	base := gate.Defaults()
	base.ReviewerProvider = "codex"
	base.ReviewerModel, base.ReviewerModelOverride = "gpt-fast", "gpt-fast"
	base.ReviewerEffort = "low"
	base.ReviewerRuns = 3

	type want struct {
		provider, model, effort string
		runs                    int
		timeout                 time.Duration
	}
	for _, tc := range []struct {
		name        string
		env         map[string]string
		file, flags config.BranchReviewer
		want        want
	}{
		{name: "unset inherits the commit reviewer with branch runs and timeout",
			want: want{"codex", "gpt-fast", "low", 1, settings.DefaultBranchTimeout}},
		{name: "same provider keeps the model and effort",
			file: config.BranchReviewer{Provider: "codex", Effort: "high"},
			want: want{"codex", "gpt-fast", "high", 1, settings.DefaultBranchTimeout}},
		{name: "another provider drops the model and effort",
			file: config.BranchReviewer{Provider: "claude-code"},
			want: want{"claude-code", "", "", 1, settings.DefaultBranchTimeout}},
		{name: "runs over the limit are capped",
			file: config.BranchReviewer{Runs: 9, Timeout: 30},
			want: want{"codex", "gpt-fast", "low", settings.MaxBranchRuns, 30 * time.Second}},
		{name: "the environment outranks the file",
			env:  map[string]string{settings.BranchModelEnv: "gpt-env", settings.BranchRunsEnv: "2"},
			file: config.BranchReviewer{Model: "gpt-file", Runs: 4},
			want: want{"codex", "gpt-env", "low", 2, settings.DefaultBranchTimeout}},
		{name: "a flag outranks the environment",
			env:   map[string]string{settings.BranchModelEnv: "gpt-env"},
			flags: config.BranchReviewer{Model: "gpt-flag"},
			want:  want{"codex", "gpt-flag", "low", 1, settings.DefaultBranchTimeout}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{settings.BranchProviderEnv, settings.BranchModelEnv,
				settings.BranchEffortEnv, settings.BranchRunsEnv, settings.BranchTimeoutEnv} {
				t.Setenv(key, tc.env[key])
			}
			cfg := settings.BranchConfig(base, tc.file, tc.flags)
			got := want{cfg.ReviewerProvider, cfg.ReviewerModelOverride, cfg.ReviewerEffort,
				cfg.ReviewerRuns, cfg.ReviewTimeout}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
			if cfg.ReviewerToolCalls <= 0 || cfg.ReviewerToolTurns <= 0 {
				t.Errorf("the branch tool caps are unset: %d calls, %d turns", cfg.ReviewerToolCalls, cfg.ReviewerToolTurns)
			}
		})
	}
}

// TestBranchRunsSettingTakesOneToTheLimit checks branch_reviewer.runs refuses a
// panel larger than MaxBranchRuns and stores one inside it.
func TestBranchRunsSettingTakesOneToTheLimit(t *testing.T) {
	f, err := settings.Find("branch_reviewer.runs")
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := f.Set(&c, "6"); err == nil {
		t.Error("branch_reviewer.runs accepted 6")
	}
	if err := f.Set(&c, "2"); err != nil || c.BranchReviewer.Runs != 2 {
		t.Errorf("Set 2: %v, stored %d", err, c.BranchReviewer.Runs)
	}
	p, _ := settings.Find("branch_reviewer.provider")
	if err := p.Set(&c, "nonesuch"); err == nil {
		t.Error("branch_reviewer.provider accepted an unknown provider")
	}
}

// TestBranchSourceNamesTheInheritedKey checks that an unset branch key reports the
// commit reviewer key it inherits, and the default once the provider differs.
func TestBranchSourceNamesTheInheritedKey(t *testing.T) {
	for _, key := range []string{"COMMIT_GATE_MODEL", settings.BranchProviderEnv, settings.BranchModelEnv} {
		t.Setenv(key, "")
	}
	model, _ := settings.Find("branch_reviewer.model")
	file := config.Config{Reviewer: config.Reviewer{Provider: "codex", Model: "gpt-fast"}}
	if got := settings.Source(model, file); got != "reviewer.model (config)" {
		t.Errorf("same provider: source %q", got)
	}
	file.BranchReviewer.Provider = "claude-code"
	if got := settings.Source(model, file); got != "default" {
		t.Errorf("another provider: source %q", got)
	}
	if got := settings.EffectiveValue(model, gate.Config{}, file); got != gate.DefaultReviewerModels["claude-code"] {
		t.Errorf("another provider: model in force %q, want its default", got)
	}
}
