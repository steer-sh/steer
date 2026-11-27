package cmd

import (
	"bytes"
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
		{"skip binary", branchreview.Listed{Review: branchreview.Review{Phase: branchreview.PhaseSkipBinary}}, "skip-binary 0 finding(s)", "nothing to rule"},
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

// TestRenderBranchReviewSaysWhatEachPhaseLeaves checks the text a review prints for
// each way it ends: a skip names why, and a finding that names a reviewed rule
// shows its RULE line.
func TestRenderBranchReviewSaysWhatEachPhaseLeaves(t *testing.T) {
	answered := []gate.ReviewRun{{}}
	finding := branchreview.Finding{Index: 1, Run: 1, Severity: "BLOCKING", File: "b.go", Claim: "B returns A.", FailsWhen: "always."}
	ruled := finding
	ruled.Rule = "global:b"
	for _, tc := range []struct {
		name       string
		res        branchreview.Result
		want, lack string
	}{
		{"skip empty", branchreview.Result{Phase: branchreview.PhaseSkipEmpty}, "Nothing to review", "Reviewer"},
		{"skip prose", branchreview.Result{Phase: branchreview.PhaseSkipProse}, "prose files only", "Reviewer"},
		{"skip binary", branchreview.Result{Phase: branchreview.PhaseSkipBinary}, "existing binary files", "Reviewer"},
		{"finding", branchreview.Result{Phase: branchreview.PhaseFindings, Answered: 1, Runs: answered,
			Review: "x", Findings: []branchreview.Finding{finding}}, "FAILS WHEN: always.\n\n", "RULE:"},
		{"rule finding", branchreview.Result{Phase: branchreview.PhaseFindings, Answered: 1, Runs: answered,
			Review: "x", Findings: []branchreview.Finding{ruled}}, "FAILS WHEN: always.\n    RULE: global:b\n\n", ""},
	} {
		var out bytes.Buffer
		renderBranchReview(&out, tc.res, gate.Defaults())
		if got := out.String(); !strings.Contains(got, tc.want) || (tc.lack != "" && strings.Contains(got, tc.lack)) {
			t.Errorf("%s: output %q; want %q without %q", tc.name, got, tc.want, tc.lack)
		}
	}
}

// TestBranchReviewCarriesTheCommitGatePolicy checks that a review with something to
// rule hands the agent the commit gate's adjudication policy, as text and as JSON, and
// a clean review does not.
func TestBranchReviewCarriesTheCommitGatePolicy(t *testing.T) {
	answered := []gate.ReviewRun{{}}
	finding := branchreview.Finding{Index: 1, Run: 1, Severity: "BLOCKING", File: "b.go", Claim: "B returns A.", FailsWhen: "always."}
	policy := gate.AdjudicationPolicy()
	for _, tc := range []struct {
		name string
		res  branchreview.Result
		want bool
	}{
		{"findings", branchreview.Result{Phase: branchreview.PhaseFindings, Answered: 1, Runs: answered,
			Review: "x", Slots: 1, Findings: []branchreview.Finding{finding}}, true},
		{"unparsed", branchreview.Result{Phase: branchreview.PhaseFindings, Answered: 1, Runs: answered,
			Review: "prose", Unparsed: 1}, true},
		{"clean", branchreview.Result{Phase: branchreview.PhaseClean, Answered: 1, Runs: answered}, false},
	} {
		var out bytes.Buffer
		renderBranchReview(&out, tc.res, gate.Defaults())
		if got := strings.Contains(out.String(), policy); got != tc.want {
			t.Errorf("%s: text carries the policy = %v, want %v", tc.name, got, tc.want)
		}
		got, ok := branchReviewJSON(tc.res, gate.Defaults())["adjudication"]
		if ok != tc.want || (ok && got != policy) {
			t.Errorf("%s: JSON adjudication = %v (present %v), want present %v", tc.name, got, ok, tc.want)
		}
	}
}
