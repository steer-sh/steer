package cadence

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
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

// A project reviewed rule has two readers: the author reads the working tree and the
// commit reviewer reads HEAD. A rule that HEAD lacks, holds with other text or paths,
// or still holds after the working tree dropped it or its review section reads changed,
// and says which; a HEAD that was not or could not be read leaves it unknown.
func TestArtifactReadsAProjectReviewedRuleAtHeadAsWellAsOnDisk(t *testing.T) {
	testhome.Set(t, t.TempDir())
	root := t.TempDir()
	store := filepath.Join(root, ".steer", "hooks")
	rec := ruleRecord("project")
	rec["tier"] = "reviewed"
	rec["rule_repo"] = identity(t, root)
	rec["rule_review_paths"] = []any{"cli/"}
	const text = "resolve config from the repo root"
	const onDisk = `{"rules":{"config-path":{"text":"` + text + `","review":{"paths":["cli/"]}}}}`
	const unreviewed = `{"rules":{"config-path":{"text":"` + text + `"}}}`
	head := func(text string, paths ...string) *gate.Addendum {
		return &gate.Addendum{Rules: []gate.ReviewedRule{{Scope: "project", ID: "config-path", Text: text, Paths: paths}}}
	}

	for _, c := range []struct {
		name, disk string
		head       *gate.Addendum
		state      string
		detail     string
	}{
		{"both hold it", onDisk, head(text, "cli/"), ArtifactPresent, ""},
		{"HEAD not read", onDisk, nil, ArtifactUnreadable, "not read"},
		{"not committed", onDisk, &gate.Addendum{}, ArtifactChanged, "HEAD does not hold it"},
		{"other text at HEAD", onDisk, head("resolve config from the cwd", "cli/"), ArtifactChanged, "copy at HEAD"},
		{"other paths at HEAD", onDisk, head(text, "docs/"), ArtifactChanged, "copy at HEAD"},
		{"removal not committed", `{"rules":{}}`, head(text, "cli/"), ArtifactChanged, "removed from the working tree"},
		{"removed from both", `{"rules":{}}`, &gate.Addendum{}, ArtifactAbsent, ""},
		{"unreviewed, not committed", unreviewed, head(text, "cli/"), ArtifactChanged, "HEAD still holds it"},
		{"unreviewed and committed", unreviewed, &gate.Addendum{}, ArtifactChanged, "so the commit reviewer does not read it"},
		{"HEAD unreadable", onDisk, &gate.Addendum{Errors: []string{"review addendum: project store: bad json"}}, ArtifactUnreadable, "bad json"},
	} {
		writeManifest(t, store, c.disk)
		got := ArtifactOf(rec, ArtifactSources{RepoRoot: root, Addendum: c.head})
		if got.State != c.state || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s: artifact = %+v; want state %q naming %q", c.name, got, c.state, c.detail)
		}
	}
}
