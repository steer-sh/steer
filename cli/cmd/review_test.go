package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/branchreview"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
)

// TestBranchProviderUsableWarnsOfAnAPIReviewerWithoutTools checks that an API
// reviewer with tools off or unset returns the diff-only warning, that one with
// tools on returns none, and that agy is refused.
func TestBranchProviderUsableWarnsOfAnAPIReviewerWithoutTools(t *testing.T) {
	for _, tc := range []struct {
		provider, tools string
		warns, refused  bool
	}{
		{reviewers.APIProviderID, "", true, false},
		{reviewers.APIProviderID, "off", true, false},
		{reviewers.APIProviderID, "auto", false, false},
		{"codex", "", false, false},
		{"agy", "", false, true},
	} {
		warning, err := branchProviderUsable(gate.Config{ReviewerProvider: tc.provider, ReviewerAPITools: tc.tools})
		if (err != nil) != tc.refused {
			t.Errorf("%s tools %q: err = %v", tc.provider, tc.tools, err)
		}
		if got := strings.Contains(warning, "reviewer.api.tools is off"); got != tc.warns {
			t.Errorf("%s tools %q: warning = %q", tc.provider, tc.tools, warning)
		}
	}
}

// TestReviewListLineSaysWhatEachReviewAwaits checks that a list line counts finding
// slots, not parsed findings, notes unparsed replies, names a skip as having nothing
// to rule, and tells a ruling on a moved HEAD apart from one on the reviewed commit.
func TestReviewListLineSaysWhatEachReviewAwaits(t *testing.T) {
	findings := branchreview.Review{Phase: branchreview.PhaseFindings, Slots: 2}
	for _, tc := range []struct {
		name          string
		row           branchreview.Listed
		count, status string
	}{
		{"awaiting", branchreview.Listed{Review: findings, Rulable: true}, "2 finding(s)", "not ruled"},
		{"unparsed", branchreview.Listed{Review: branchreview.Review{Phase: branchreview.PhaseFindings, Slots: 3, Unparsed: 1}},
			"3 finding(s), 1 reply(ies) unparsed", "cannot be ruled one by one"},
		{"skip", branchreview.Listed{Review: branchreview.Review{Phase: branchreview.PhaseSkipEmpty}}, "0 finding(s)", "nothing to rule"},
		{"ruled", branchreview.Listed{Review: findings, Rulable: true, Ruled: true}, "2 finding(s)", "  ruled"},
		{"advanced", branchreview.Listed{Review: findings, Rulable: true, Ruled: true, HeadAdvanced: true},
			"2 finding(s)", "ruled, HEAD moved after review"},
		{"elsewhere", branchreview.Listed{Review: findings, Rulable: true, Ruled: true, HeadElsewhere: true},
			"2 finding(s)", "ruled, HEAD moved off the reviewed commit"},
	} {
		if line := reviewListLine(tc.row); !strings.Contains(line, tc.count+"  ") || !strings.HasSuffix(line, tc.status) {
			t.Errorf("%s: line %q; want %q and the status %q", tc.name, line, tc.count, tc.status)
		}
	}
}
