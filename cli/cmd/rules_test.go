package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steer-sh/steer-cli/internal/claude"
)

// A hand-edited manifest can carry a short or false rule_hash stamp. The list
// names the digest of the stored text, so it neither repeats the stamp nor fails
// to trim it.
// pins existing behaviour
func TestRulesListNamesTheDigestOfTheStoredText(t *testing.T) {
	t.Chdir(hooksScratchRepo(t))
	const text = "use python3 instead"
	if _, err := claude.InstallManagedRule(claude.ProjectScope, "py3", text); err != nil {
		t.Fatal(err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Rules["py3"]
	entry.RuleHash = "abc"
	manifest.Rules["py3"] = entry
	if err := claude.SaveManifest(claude.ProjectScope, manifest); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() { runErr = rulesListCmd.RunE(rulesListCmd, nil) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if want := "rule_hash " + claude.HashRule(text)[:16]; !strings.Contains(out, want) {
		t.Fatalf("list = %q, want %q", out, want)
	}
}

// A change to a project reviewed rule reaches the commit reviewer only once the store's
// manifest is committed, so adding one says so, and unreviewing or removing one says so
// while HEAD holds it as a reviewed rule; an instruction rule says nothing.
func TestAProjectReviewedRuleChangeAsksForTheManifestCommit(t *testing.T) {
	repo := hooksScratchRepo(t)
	t.Chdir(repo)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	run := func(c *cobra.Command, args []string, flags map[string]string) string {
		t.Helper()
		for name, value := range flags {
			if err := c.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		// Each step starts from the defaults, so no flag leaks into the next step.
		defer c.Flags().VisitAll(func(f *pflag.Flag) {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
		var runErr error
		out := captureStdout(t, func() { runErr = c.RunE(c, args) })
		if runErr != nil {
			t.Fatal(runErr)
		}
		return out
	}
	const added, kept = "on its own, before the change", "goes on reading this rule until you commit"

	reviewed := map[string]string{"rule": "resolve config from the repo root", "review-path": "cli/"}
	steps := []struct {
		name     string
		cmd      *cobra.Command
		flags    map[string]string
		want     string
		wantNone bool
		commit   bool
	}{
		{"add reviewed", rulesAddCmd, reviewed, added, false, true},
		{"unreview", rulesAddCmd, map[string]string{"rule": "resolve config from the repo root", "no-review": "true"}, kept, false, true},
		{"review again", rulesAddCmd, reviewed, added, false, false},
		{"remove a rule HEAD holds unreviewed", rulesRemoveCmd, nil, "", true, false},
		{"add reviewed once more", rulesAddCmd, reviewed, added, false, true},
		{"remove", rulesRemoveCmd, nil, kept, false, false},
		{"add reviewed for the last time", rulesAddCmd, reviewed, added, false, true},
		{"unreview, not committed", rulesAddCmd, map[string]string{"rule": "resolve config from the repo root", "no-review": "true"}, kept, false, false},
		{"remove an unreviewed rule HEAD holds as reviewed", rulesRemoveCmd, nil, kept, false, false},
	}
	for _, s := range steps {
		out := run(s.cmd, []string{"cfg"}, s.flags)
		if s.commit {
			gitIn(t, repo, "add", "-A")
			gitIn(t, repo, "commit", "-q", "-m", s.name)
		}
		switch {
		case s.wantNone && strings.Contains(out, "commit gate reads project rules"):
			t.Errorf("%s: a rule HEAD does not hold as reviewed asked for a commit:\n%s", s.name, out)
		case !s.wantNone && !strings.Contains(out, s.want):
			t.Errorf("%s: output lacks %q:\n%s", s.name, s.want, out)
		}
	}
}
