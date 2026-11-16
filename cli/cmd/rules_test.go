package cmd

import (
	"strings"
	"testing"

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
