package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// TestAPinnedRegistrationIsReportedThenStripped pins commit 7's whole point: a
// registration written by an older steer names the reviewer on the command line,
// which outranks ~/.steer/config.json. Until it is stripped, every `steer config
// set` changes nothing while `status` reports the new value.
func TestAPinnedRegistrationIsReportedThenStripped(t *testing.T) {
	tempSettingsHome(t)
	if err := claude.RegisterCommitGateHook(claude.GlobalScope,
		"steer commit-gate review --provider agy --model gemini-3.7-flash-high", 210); err != nil {
		t.Fatal(err)
	}

	pinned := pinnedRegistrations()
	if len(pinned) != 1 {
		t.Fatalf("pinnedRegistrations found %d, want 1", len(pinned))
	}
	if pinned[0].Repair != "steer bin install" {
		t.Fatalf("repair = %q, want the bin install command", pinned[0].Repair)
	}

	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repointRegistrations(path); err != nil {
		t.Fatal(err)
	}

	arming := claude.CommitGateHookArmed(claude.GlobalScope)
	want := steerbin.Quote(path) + " commit-gate review"
	if arming.Command != want {
		t.Fatalf("command = %q, want %q", arming.Command, want)
	}
	if arming.Timeout != 210 {
		t.Fatalf("timeout = %d, want 210 preserved", arming.Timeout)
	}
	if len(pinnedRegistrations()) != 0 {
		t.Fatal("a stripped registration must no longer read as pinning the reviewer")
	}

	// The stripped hook reads the settings file, so a config edit now reaches it.
	setSetting(t, "reviewer.provider", "codex")
	cfg, reviewer, err := resolvedGateConfig()
	if err != nil {
		t.Fatalf("resolvedGateConfig: %v", err)
	}
	if cfg.ReviewerProvider != "codex" || reviewer.Name() != "codex" {
		t.Fatalf("reviewer = %s/%s; want codex", cfg.ReviewerProvider, reviewer.Name())
	}
}

// TestAFreshRegistrationNamesNoReviewer: install writes the bare command, so the
// settings file is the only place the reviewer is named.
func TestAFreshRegistrationNamesNoReviewer(t *testing.T) {
	tempSettingsHome(t)
	oldProvider, oldModel := commitGateProvider, commitGateModel
	commitGateProvider, commitGateModel = "codex", "gpt-reviewer"
	t.Cleanup(func() { commitGateProvider, commitGateModel = oldProvider, oldModel })

	for _, command := range []string{commitGateCommand(), commitGateCodexCommand()} {
		if steerbin.HasFlags(command, reviewerFlags...) {
			t.Fatalf("registration command %q names the reviewer", command)
		}
	}
	// The script is many lines, so its steer line is read on its own: a whole-script
	// test would find no steer command at the front and pass on anything.
	var invocation string
	for _, line := range strings.Split(buildCommitGatePreCommitScript(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "steer commit-gate precommit") {
			invocation = strings.TrimSpace(line)
		}
	}
	if invocation != "steer commit-gate precommit" {
		t.Fatalf("pre-commit invocation = %q, want the bare command", invocation)
	}
}
