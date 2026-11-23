package mine

import (
	"fmt"
	"io"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// ReviewActivity is what the commit reviewer did with a reviewed rule in the
// after-window, read from the ledger rows of the window's eligible review chains. A
// finding that names the rule is a catch the reviewer made; on the review-findings
// corpus the same finding also counts as recurrence when the record's regex admits it.
type ReviewActivity struct {
	// Applicable is true for a reviewed-tier record that names its rule.
	Applicable bool   `json:"applicable"`
	RuleKey    string `json:"rule_key,omitempty"`
	// Unplaced marks a project rule whose record names no repository: every
	// repository's rule with its id shares the key, so nothing is counted.
	Unplaced bool `json:"unplaced"`
	// Chains is how many eligible after-window review chains there were, in the rule's
	// own repository for a project rule. With none, every count below is absent
	// opportunity, never a quiet rule.
	Chains int `json:"chains"`
	// ReviewedRounds is how many rounds a reviewer answered with the rule in its prompt.
	ReviewedRounds int `json:"reviewed_rounds"`
	// FindingRounds is how many of those rounds held a finding naming the rule, and
	// FindingChains how many distinct chains held one.
	FindingRounds int `json:"finding_rounds"`
	FindingChains int `json:"finding_chains"`
	// Findings counts the findings naming the rule by severity; a NITPICK did not block.
	Findings map[string]int `json:"findings"`
	NewestTS float64        `json:"newest_ts,omitempty"`
}

// reviewActivityOf reads a reviewed rule's activity over the given eligible
// after-window chains. A record on another tier reads not applicable.
func reviewActivityOf(record Record, after []Chain) ReviewActivity {
	a := ReviewActivity{Findings: map[string]int{}}
	id, scope := str(record["rule_id"]), str(record["rule_scope"])
	if !tier.IsReviewed(str(record["tier"])) || id == "" || scope == "" {
		return a
	}
	a.Applicable = true
	a.RuleKey = scope + ":" + id
	// Every repository's project rule with this id shares the key, so a project rule
	// counts only the chains of the repository it was frozen in.
	repo := ""
	if scope == steerpath.ProjectScope {
		if repo = str(record["rule_repo"]); repo == "" {
			a.Unplaced = true
			return a
		}
	}
	for _, chain := range after {
		if repo != "" && !sameRepoRoot(chain.RepoRoot, repo) {
			continue
		}
		a.Chains++
		caught := false
		for _, row := range chain.Rows {
			if gate.NonReviewPhases[str(row["phase"])] || numFloat(row["reviewers"]) < 1 ||
				!holdsString(row["reviewed_rules_applied"], a.RuleKey) {
				continue
			}
			a.ReviewedRounds++
			bySeverity := ruleSeverities(row["rule_findings"], a.RuleKey)
			if len(bySeverity) == 0 {
				continue
			}
			a.FindingRounds++
			caught = true
			for severity, n := range bySeverity {
				a.Findings[severity] += n
			}
			if ts := numFloat(row["ts"]); ts > a.NewestTS {
				a.NewestTS = ts
			}
		}
		if caught {
			a.FindingChains++
		}
	}
	return a
}

// holdsString reports whether a decoded JSON list holds want.
func holdsString(v any, want string) bool {
	switch list := v.(type) {
	case []any:
		for _, x := range list {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range list {
			if s == want {
				return true
			}
		}
	}
	return false
}

// ruleSeverities reads one rule's per-severity counts from a row's rule_findings.
func ruleSeverities(v any, key string) map[string]int {
	out := map[string]int{}
	switch byRule := v.(type) {
	case map[string]any:
		if counts, ok := byRule[key].(map[string]any); ok {
			for severity, n := range counts {
				if c := int(numFloat(n)); c > 0 {
					out[strings.ToUpper(severity)] += c
				}
			}
		}
	case map[string]map[string]int:
		for severity, n := range byRule[key] {
			if n > 0 {
				out[strings.ToUpper(severity)] += n
			}
		}
	}
	return out
}

// sessionReviewActivity is reviewActivityOf for a session-corpus record: the review
// chains it reads are the after-window chains its own scope admits and whose evidence
// is settled at clock.
func sessionReviewActivity(record Record, ev EvidenceSet, scope *Scope, afterStart, clock float64) ReviewActivity {
	if !tier.IsReviewed(str(record["tier"])) {
		return reviewActivityOf(record, nil)
	}
	_, after := SplitChains(ev.Chains, afterStart)
	return reviewActivityOf(record, CoverEvidence(after, scope, clock).EligibleChains())
}

// writeReviewActivity prints a reviewed rule's activity line for `steer fix score`.
func writeReviewActivity(w io.Writer, a ReviewActivity) {
	switch {
	case !a.Applicable:
		return
	case a.Unplaced:
		fmt.Fprintf(w, "    review: %s not counted — the record names no repository\n", a.RuleKey)
	case a.Chains == 0:
		fmt.Fprintf(w, "    review: %s — no eligible review chain since the split\n", a.RuleKey)
	default:
		fmt.Fprintf(w, "    review: %s applied in %d answered rounds over %d chains; %d rounds in %d chains named it (%s)\n",
			a.RuleKey, a.ReviewedRounds, a.Chains, a.FindingRounds, a.FindingChains, severityList(a.Findings))
	}
}

func severityList(counts map[string]int) string {
	var parts []string
	for _, s := range []string{"BLOCKING", "SUBSTANTIVE", "NITPICK"} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	if len(parts) == 0 {
		return "no findings"
	}
	return strings.Join(parts, ", ")
}
