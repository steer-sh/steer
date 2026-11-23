package cadence

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// A reviewed rule is read against its frozen paths as well as its text: the same
// section is present, other paths or no section is changed, and the reason says which.
func TestArtifactReadsAReviewedRuleAgainstItsFrozenPaths(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	store := filepath.Join(home, ".steer", "hooks")
	rec := ruleRecord("global")
	rec["tier"] = "reviewed"
	rec["rule_review_paths"] = []any{"cli/"}
	const text = `"text":"resolve config from the repo root"`

	writeManifest(t, store, `{"rules":{"config-path":{`+text+`,"review":{"paths":["cli/"]}}}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactPresent)

	for body, want := range map[string]string{
		`{"rules":{"config-path":{` + text + `,"review":{"paths":["docs/"]}}}}`: "docs/",
		`{"rules":{"config-path":{` + text + `}}}`:                              "no longer a reviewed rule",
	} {
		writeManifest(t, store, body)
		got := ArtifactOf(rec, ArtifactSources{})
		wantState(t, got, ArtifactChanged)
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail = %q, want it to name %q", got.Detail, want)
		}
	}
}
