package claude

import (
	"reflect"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

func storedReview(t *testing.T, scope, id string) *RuleReview {
	t.Helper()
	manifest, err := LoadManifest(scope)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Rules[id].Review
}

// A review section is stored with sorted, distinct paths; a plain rewording keeps it,
// and ClearReview makes the rule an instruction rule again.
func TestAReviewedRuleKeepsItsSectionUntilCleared(t *testing.T) {
	withProjectTempDir(t)
	opts := RuleOptions{Review: &RuleReview{Paths: []string{" *.go", "cli/internal/tui/", "*.go"}}}
	if _, err := InstallRule(ProjectScope, "tui", "tui reads status", opts); err != nil {
		t.Fatal(err)
	}
	want := &RuleReview{Paths: []string{"*.go", "cli/internal/tui/"}}
	if got := storedReview(t, ProjectScope, "tui"); !reflect.DeepEqual(got, want) {
		t.Fatalf("review = %+v, want %+v", got, want)
	}
	if _, err := InstallManagedRule(ProjectScope, "tui", "tui reads health from status"); err != nil {
		t.Fatal(err)
	}
	if got := storedReview(t, ProjectScope, "tui"); !reflect.DeepEqual(got, want) {
		t.Fatalf("rewording dropped the review section: %+v", got)
	}
	if _, err := InstallRule(ProjectScope, "tui", "tui reads health from status", RuleOptions{ClearReview: true}); err != nil {
		t.Fatal(err)
	}
	if got := storedReview(t, ProjectScope, "tui"); got != nil {
		t.Fatalf("ClearReview left %+v", got)
	}
}

// A project reviewed rule must name its paths and each path must be in the grammar;
// a global rule may name none.
func TestAReviewedRuleRefusesPathsOutsideItsScope(t *testing.T) {
	testhome.Set(t, withProjectTempDir(t))
	for _, paths := range [][]string{nil, {"cli/**"}} {
		_, err := InstallRule(ProjectScope, "r", "a rule", RuleOptions{Review: &RuleReview{Paths: paths}})
		if err == nil || !strings.Contains(err.Error(), "review") {
			t.Errorf("paths %v: err = %v, want a refusal", paths, err)
		}
	}
	if got := storedReview(t, ProjectScope, "r"); got != nil {
		t.Fatalf("a refused install stored %+v", got)
	}
	if _, err := InstallRule(GlobalScope, "g", "a global rule", RuleOptions{Review: &RuleReview{}}); err != nil {
		t.Fatalf("a global reviewed rule with no paths: %v", err)
	}
	if got := storedReview(t, GlobalScope, "g"); got == nil || len(got.Paths) != 0 {
		t.Fatalf("global review = %+v", got)
	}
}

// A removed reviewed rule comes back from the archive with its review section.
func TestRestoreBringsBackAReviewedRulesSection(t *testing.T) {
	withProjectTempDir(t)
	opts := RuleOptions{Review: &RuleReview{Paths: []string{"cli/internal/tui/"}}}
	if _, err := InstallRule(ProjectScope, "tui", "tui reads status", opts); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedRule(ProjectScope, "tui", "testing restore"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreArchived(ProjectScope, onlyEntry(t)); err != nil {
		t.Fatal(err)
	}
	if got := storedReview(t, ProjectScope, "tui"); !reflect.DeepEqual(got, opts.Review) {
		t.Fatalf("restored review = %+v", got)
	}
}
