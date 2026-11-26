package cmd

import (
	"strings"
	"testing"

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
