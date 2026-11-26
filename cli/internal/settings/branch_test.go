package settings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/settings"
)

// TestBranchConfigInheritsAndOverrides checks each layer of the branch reviewer's
// precedence: commit reviewer, file, environment, flags, the branch's own effort
// default for a CLI reviewer alone, and the drop of the model and effort beneath
// any layer that changes the provider.
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
		{name: "unset inherits the commit reviewer with branch effort, runs and timeout",
			want: want{"codex", "gpt-fast", settings.DefaultBranchEffort, 1, settings.DefaultBranchTimeout}},
		{name: "same provider keeps the model and takes a set effort",
			file: config.BranchReviewer{Provider: "codex", Effort: "medium"},
			want: want{"codex", "gpt-fast", "medium", 1, settings.DefaultBranchTimeout}},
		{name: "another provider drops the model and keeps the branch effort",
			file: config.BranchReviewer{Provider: "claude-code"},
			want: want{"claude-code", "", settings.DefaultBranchEffort, 1, settings.DefaultBranchTimeout}},
		{name: "an API reviewer gets no branch effort default",
			file: config.BranchReviewer{Provider: "openai-compatible"},
			want: want{"openai-compatible", "", "", 1, settings.DefaultBranchTimeout}},
		{name: "runs over the limit are capped",
			file: config.BranchReviewer{Runs: 9, Timeout: 30},
			want: want{"codex", "gpt-fast", settings.DefaultBranchEffort, settings.MaxBranchRuns, 30 * time.Second}},
		{name: "the environment outranks the file",
			env:  map[string]string{settings.BranchModelEnv: "gpt-env", settings.BranchRunsEnv: "2"},
			file: config.BranchReviewer{Model: "gpt-file", Runs: 4},
			want: want{"codex", "gpt-env", settings.DefaultBranchEffort, 2, settings.DefaultBranchTimeout}},
		{name: "a flag outranks the environment",
			env:   map[string]string{settings.BranchModelEnv: "gpt-env"},
			flags: config.BranchReviewer{Model: "gpt-flag"},
			want:  want{"codex", "gpt-flag", settings.DefaultBranchEffort, 1, settings.DefaultBranchTimeout}},
		{name: "a flag's provider drops the file's model",
			file:  config.BranchReviewer{Model: "gpt-file"},
			flags: config.BranchReviewer{Provider: "claude-code"},
			want:  want{"claude-code", "", settings.DefaultBranchEffort, 1, settings.DefaultBranchTimeout}},
		{name: "a flag switching back to the commit provider drops the file's model",
			file:  config.BranchReviewer{Provider: "claude-code", Model: "claude-file"},
			flags: config.BranchReviewer{Provider: "codex"},
			want:  want{"codex", "", settings.DefaultBranchEffort, 1, settings.DefaultBranchTimeout}},
		{name: "the environment's provider drops the file's effort",
			env:  map[string]string{settings.BranchProviderEnv: "openai-compatible"},
			file: config.BranchReviewer{Provider: "claude-code", Effort: "max"},
			want: want{"openai-compatible", "", "", 1, settings.DefaultBranchTimeout}},
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

// TestBranchProviderSettingRefusesAgy checks that branch_reviewer.provider refuses
// agy with the reason a branch review gives, and that its choices and usage omit it.
func TestBranchProviderSettingRefusesAgy(t *testing.T) {
	f, err := settings.Find("branch_reviewer.provider")
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := f.Set(&c, " AGY "); err == nil || err.Error() != settings.BranchProviderRefusal("agy").Error() {
		t.Errorf("Set agy: %v; want the branch refusal", err)
	}
	if err := f.Set(&c, "codex"); err != nil || c.BranchReviewer.Provider != "codex" {
		t.Errorf("Set codex: %v, stored %q", err, c.BranchReviewer.Provider)
	}
	for _, choice := range f.Choices() {
		if choice.Value == "agy" {
			t.Errorf("choices offer agy: %v", f.Choices())
		}
	}
	if len(f.Choices()) != len(reviewers.Providers())-1 {
		t.Errorf("choices %v; want every provider but agy", f.Choices())
	}
	if err := f.Set(&c, "nonesuch"); err == nil || strings.Contains(err.Error(), "agy") {
		t.Errorf("Set nonesuch: %v; want a refusal that does not offer agy", err)
	}
	if strings.Contains(f.Usage, "agy") || !strings.Contains(f.Usage, "codex") {
		t.Errorf("usage %q; want codex and no agy", f.Usage)
	}
}

// TestBranchEffortWarningReadsTheBranchReviewerInForce checks that the warning
// judges the branch effort against the branch provider, not the commit reviewer's.
func TestBranchEffortWarningReadsTheBranchReviewerInForce(t *testing.T) {
	for _, key := range []string{"COMMIT_GATE_PROVIDER", "COMMIT_GATE_EFFORT", settings.BranchProviderEnv, settings.BranchEffortEnv} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name  string
		file  config.Config
		warns bool
	}{
		{"default", config.Config{}, false},
		{"undocumented", config.Config{BranchReviewer: config.BranchReviewer{Effort: "extreme"}}, true},
		{"documented for the branch provider", config.Config{
			Reviewer:       config.Reviewer{Provider: "claude-code"},
			BranchReviewer: config.BranchReviewer{Provider: "pi", Effort: "minimal"}}, false},
		{"undocumented for the branch provider", config.Config{
			Reviewer:       config.Reviewer{Provider: "pi"},
			BranchReviewer: config.BranchReviewer{Provider: "claude-code", Effort: "minimal"}}, true},
	} {
		got := settings.BranchEffortWarning(tc.file)
		if (got != "") != tc.warns || (tc.warns && !strings.HasPrefix(got, "branch reviewer effort")) {
			t.Errorf("%s: warning %q", tc.name, got)
		}
	}
}

// TestBranchSourceNamesTheInheritedKey checks that an unset branch key reports the
// commit reviewer key it inherits, and the default once the provider differs or
// the environment's provider discards the stored value.
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
	t.Setenv(settings.BranchProviderEnv, "codex")
	if got := settings.Source(model, file); got != "default" {
		t.Errorf("switched away and back: source %q", got)
	}
	file.BranchReviewer.Model = "claude-file"
	if got := settings.Source(model, file); got != "default" {
		t.Errorf("stored model under another environment provider: source %q", got)
	}
	t.Setenv(settings.BranchProviderEnv, "")
	if got := settings.Source(model, file); got != "config" {
		t.Errorf("stored model: source %q", got)
	}
}
