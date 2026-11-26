//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// branchSandbox is a sandbox with codex as the commit reviewer and HEAD on a
// feature branch that adds code over main.
func branchSandbox(t *testing.T) *Sandbox {
	t.Helper()
	s := NewSandbox(t)
	s.Reviewer("codex", "model-codex", "", 1, 20)
	s.Git("checkout", "-q", "-b", "feature")
	s.StageCode("mod.py")
	s.Git("commit", "-q", "-m", "add mod")
	return s
}

// reviewBranch runs `steer review branch --json` with args, requires exit 0 and
// decodes the result.
func (s *Sandbox) reviewBranch(args ...string) map[string]any {
	s.t.Helper()
	r := s.MustSteer(append([]string{"review", "branch", "--json"}, args...)...)
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		s.t.Fatalf("review branch --json: %v\n%s", err, r.Stdout)
	}
	return out
}

// branchReviewRows reads the sandbox's branch-review rows.
func (s *Sandbox) branchReviewRows() []map[string]any {
	s.t.Helper()
	return readJSONL(s.t, filepath.Join(s.Home, ".steer", "state", "branch-review", "reviews.e2e.jsonl"))
}

// listedReview runs `steer review list --json` and returns its one row.
func (s *Sandbox) listedReview() map[string]any {
	s.t.Helper()
	r := s.MustSteer("review", "list", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &rows); err != nil || len(rows) != 1 {
		s.t.Fatalf("review list --json: %v, %d row(s):\n%s", err, len(rows), r.Stdout)
	}
	return rows[0]
}

// TestBranchReviewIsRuledAndListed runs a branch review with three findings, refuses
// rulings that miss or repeat a finding, records valid ones, and lists the review as
// ruled, then as ruled after HEAD advanced once a later commit is ruled again.
//
// pins existing behaviour
func TestBranchReviewIsRuledAndListed(t *testing.T) {
	t.Parallel()
	s := branchSandbox(t)
	s.Script("findings")
	out := s.reviewBranch()
	id, _ := out["review_id"].(string)
	if id == "" || out["phase"] != "findings" {
		t.Fatalf("review_id %q phase %v, want an id and findings:\n%v", id, out["phase"], out)
	}
	findings, _ := out["findings"].([]any)
	for i, f := range findings {
		if f.(map[string]any)["index"] != float64(i+1) {
			t.Errorf("finding %d has index %v", i+1, f.(map[string]any)["index"])
		}
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %v, want three", findings)
	}
	if rows := s.branchReviewRows(); len(rows) != 1 || rows[0]["review_id"] != id {
		t.Fatalf("branch-review rows = %v, want one for %s", rows, id)
	}
	if calls := s.Calls(); len(calls) != 1 || calls[0].Provider != "codex" {
		t.Fatalf("stub calls = %v, want one codex call", calls)
	}

	ruling := func(indexes ...int) string {
		var rs []map[string]any
		for _, i := range indexes {
			rs = append(rs, map[string]any{"index": i, "verdict": "REJECTED", "resolution": "The stub's premise is false."})
		}
		data, _ := json.Marshal(map[string]any{"rulings": rs})
		return string(data)
	}
	rule := func(stdin string) Result {
		t.Helper()
		return s.Run(s.Repo, stdin, nil, "steer", "review", "rule", id, "--stdin", "--json")
	}
	for _, tc := range []struct {
		name    string
		indexes []int
		want    string
	}{
		{"missing", []int{1, 2}, "unruled: 3"},
		{"duplicate", []int{1, 1, 2, 3}, "finding 1 is ruled twice"},
	} {
		if r := rule(ruling(tc.indexes...)); r.Code == 0 || !strings.Contains(r.Output(), tc.want) {
			t.Errorf("%s ruling exited %d, want non-zero naming %q:\n%s", tc.name, r.Code, tc.want, r.Output())
		}
	}
	if l := s.listedReview(); l["ruled"] != false || l["rulable"] != true {
		t.Fatalf("after refused rulings, listed = %v, want rulable and not ruled", l)
	}

	for _, tc := range []struct {
		name     string
		commit   bool
		advanced bool
		status   string
	}{
		{"at the reviewed head", false, false, "ruled"},
		{"after a fix commit", true, true, "ruled, HEAD moved after review"},
	} {
		if tc.commit {
			s.StageCode("fix.py")
			s.Git("commit", "-q", "-m", "fix")
		}
		r := rule(ruling(1, 2, 3))
		if r.Code != 0 {
			t.Fatalf("%s: rule exited %d:\n%s", tc.name, r.Code, r.Output())
		}
		var receipt map[string]any
		if err := json.Unmarshal([]byte(r.Stdout), &receipt); err != nil || receipt["rejected"] != float64(3) ||
			receipt["head_advanced"] != tc.advanced {
			t.Fatalf("%s: receipt %v (%v), want 3 rejected and head_advanced %v", tc.name, receipt, err, tc.advanced)
		}
		if l := s.listedReview(); l["ruled"] != true || l["head_advanced"] != tc.advanced || l["finding_slots"] != float64(3) {
			t.Errorf("%s: listed = %v, want ruled, head_advanced %v, 3 slots", tc.name, l, tc.advanced)
		}
		text := strings.TrimSpace(s.MustSteer("review", "list").Stdout)
		if !strings.HasPrefix(text, id) || !strings.HasSuffix(text, "3 finding(s)  "+tc.status) {
			t.Errorf("%s: review list = %q, want %s ending in 3 finding(s) and %q", tc.name, text, id, tc.status)
		}
	}
}

// TestBranchReviewConfigReachesTheRun checks what the branch settings put on the
// reviewer's command line and which pre-run warnings reach --json warnings[] and the
// row's review_errors.
//
// pins existing behaviour
func TestBranchReviewConfigReachesTheRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		setup    func(s *Sandbox)
		args     []string
		provider string
		model    string
		effort   string
		warning  string
	}{
		{
			name:     "an undocumented effort warns",
			args:     []string{"--provider", "claude-code", "--effort", "bogus"},
			provider: "claude", model: "claude-haiku-5-5", effort: "bogus",
			warning: `reviewer effort "bogus" is not one of claude-code's documented levels`,
		},
		{
			name: "a broken settings file warns",
			setup: func(s *Sandbox) {
				if err := os.WriteFile(filepath.Join(s.Home, ".steer", "config.json"), []byte("{not json"), 0o600); err != nil {
					s.t.Fatal(err)
				}
			},
			args:     []string{"--provider", "codex"},
			provider: "codex", model: "gpt-6-luna", effort: "high",
			warning: "using the shipped defaults",
		},
		{
			name:     "a provider flag drops the file's branch model",
			setup:    func(s *Sandbox) { s.MustSteer("config", "set", "branch_reviewer.model", "branch-codex-model") },
			args:     []string{"--provider", "claude-code"},
			provider: "claude", model: "claude-haiku-5-5", effort: "high",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := branchSandbox(t)
			if tc.setup != nil {
				tc.setup(s)
			}
			s.Script("finding")
			out := s.reviewBranch(tc.args...)
			calls := s.Calls()
			if len(calls) != 1 {
				t.Fatalf("stub calls = %d, want 1", len(calls))
			}
			if c := calls[0]; c.Provider != tc.provider || c.Model != tc.model || c.Effort != tc.effort {
				t.Errorf("call = %s model %q effort %q, want %s model %q effort %q",
					c.Provider, c.Model, c.Effort, tc.provider, tc.model, tc.effort)
			}
			rows := s.branchReviewRows()
			if len(rows) != 1 {
				t.Fatalf("branch-review rows = %d, want 1", len(rows))
			}
			for where, list := range map[string]any{"warnings": out["warnings"], "review_errors": rows[0]["review_errors"]} {
				items, _ := list.([]any)
				found := slices.ContainsFunc(items, func(w any) bool {
					str, _ := w.(string)
					return tc.warning != "" && strings.Contains(str, tc.warning)
				})
				if tc.warning == "" && len(items) > 0 {
					t.Errorf("%s = %v, want none", where, items)
				} else if tc.warning != "" && !found {
					t.Errorf("%s = %v, want one naming %q", where, items, tc.warning)
				}
			}
		})
	}
}

// TestBranchProviderRefusesAgyAtSet checks that config set refuses agy as the branch
// reviewer and leaves the stored provider as it was.
//
// pins existing behaviour
func TestBranchProviderRefusesAgyAtSet(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.MustSteer("config", "set", "branch_reviewer.provider", "codex")
	r := s.Steer("config", "set", "branch_reviewer.provider", "agy")
	if r.Code == 0 || !strings.Contains(r.Output(), "cannot review a branch") {
		t.Fatalf("config set branch_reviewer.provider agy exited %d, want a refusal:\n%s", r.Code, r.Output())
	}
	data, err := os.ReadFile(filepath.Join(s.Home, ".steer", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil || dig(cfg, "branch_reviewer", "provider") != "codex" {
		t.Errorf("config.json = %s (%v), want branch_reviewer.provider codex", data, err)
	}
}
