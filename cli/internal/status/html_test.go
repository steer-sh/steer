package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

func fptr(f float64) *float64 { return &f }
func iptr(n int) *int         { return &n }

// sample is a populated snapshot covering every branch the page renders: a live
// effective class, a flagged one, a candidate, an unscored record and a chain.
func sample() *SystemStatus {
	return &SystemStatus{
		StatusVersion: StatusVersion,
		GeneratedAt:   "2026-01-01T00:00:00Z",
		Posture: Posture{
			Instrument: "agy@sha", Reviewer: "gemini", Ledger: "/l.jsonl",
			CommitGate: []ScopeArming{
				{Harness: "claude-code", Scope: "project", Registered: false, Trust: TrustNotApplicable},
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true, Timeout: 90,
					Trust: TrustNotApplicable},
				{Harness: "codex", Scope: "global", Registered: true, Effective: true, Timeout: 90,
					Trust: TrustRecordPresent},
			},
			Denials: []ScopeArming{{Harness: "claude-code", Scope: "global", Registered: true,
				Trust: TrustNotApplicable}},
			Resolver: []ScopeArming{{Harness: "claude-code", Scope: "global", Registered: true,
				Effective: true, Timeout: 30, Trust: TrustNotApplicable}},
			// Unresolved rows are why a window can read evidence-incomplete, so the
			// page must name the count and the command that clears it.
			Unresolved: 4,
		},
		Interventions: []ScoredIntervention{
			{ID: "widget-flag", Class: "widget flag loss", Tier: "check", Target: "check.sh",
				Status: "active", HookID: "h1", Verdict: "effective",
				Before:         cadence.Incidence{Hits: 12, Units: 12, Rate: 1},
				After:          cadence.Incidence{Hits: 0, Units: 12, Rate: 0},
				Scope:          &cadence.Scope{Version: 1, Repo: "steer.*", Path: `\.go$`},
				BeforeCoverage: cadence.Coverage{Total: 14, Eligible: 12, Ineligible: 2},
				AfterCoverage:  cadence.Coverage{Total: 13, Eligible: 12, Unknown: 1, Pending: 1},
				Effect: cadence.Effect{Estimate: fptr(0), Upper: fptr(0.31),
					Confidence: 0.9, Method: "clopper-pearson / mixture-cs"},
				RequiredAfter: iptr(9),
				Composition: []mine.Confound{{Category: "host", Reason: "host set changed",
					Before: []string{"a"}, After: []string{"a", "b"}}}},
			{ID: "bare-python", Class: "bare python call", Tier: "guard", Target: "steer",
				Status: "active", HookID: "no-bare-python", Verdict: "not-triggered",
				Corpus: mine.CorpusTranscript, Unit: mine.UnitSession, SignalKind: mine.SignalCorrection,
				Before:         cadence.Incidence{Hits: 2, Units: 20, Rate: 0.1},
				After:          cadence.Incidence{Hits: 5, Units: 20, Rate: 0.25},
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 20, Eligible: 20},
				AfterCoverage:  cadence.Coverage{Total: 22, Eligible: 20, PartialExposure: 2},
				Attribution: cadence.Attribution{
					Applicable: true, LogPresent: true, HookID: "no-bare-python"},
				Effect: cadence.Effect{Estimate: fptr(2.5), Lower: 1.4, Upper: fptr(4.2),
					Confidence: 0.9, Method: "clopper-pearson / mixture-cs"}},
			{ID: "drifted-gate", Class: "drifted gate", Tier: "review", Target: "prompts",
				Status: "active", Verdict: "confounded",
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 8, Eligible: 8},
				AfterCoverage:  cadence.Coverage{Total: 6, Eligible: 6},
				Confounds: []mine.Confound{{Category: "instrument", Reason: "after-window reviewed by another instrument",
					Before: []string{"agy@sha"}, After: []string{"agy@other"}}}},
			{ID: "lost-ledger", Class: "lost ledger", Tier: "guard", Target: "guard.py",
				Status: "active", Verdict: "evidence-incomplete",
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 5, Eligible: 5},
				AfterCoverage:  cadence.Coverage{Total: 4, Unknown: 4, UnknownEvidence: 4}},
			{ID: "taste-rule", Class: "naming taste", Tier: "instruction", Target: "CLAUDE.md",
				Status: "rejected", Verdict: "unscored"},
		},
		Summary: VerdictSummary{Total: 5, Counts: map[string]int{
			"effective": 1, "ineffective": 1, "confounded": 1,
			"evidence-incomplete": 1, "unscored": 1}},
		Chains: []ChainSummary{{Host: "h", StartTS: 1700000000, Rounds: 2, Files: 3,
			Findings: 2, Accepted: 1, Rejected: 1, Sessions: 1, Terminal: "clean"}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{{
				ID: "bare-python", Class: "bare python call", Tier: "guard", Target: "steer",
				Verdict:        "ineffective",
				Before:         cadence.Incidence{Hits: 2, Units: 20, Rate: 0.1},
				After:          cadence.Incidence{Hits: 5, Units: 20, Rate: 0.25},
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 20, Eligible: 20},
				AfterCoverage:  cadence.Coverage{Total: 20, Eligible: 20},
				Effect: cadence.Effect{Estimate: fptr(2.5), Lower: 1.4, Upper: fptr(4.2),
					Confidence: 0.9, Method: "clopper-pearson / mixture-cs"},
			}, {
				ID: "drifted-gate", Class: "drifted gate", Tier: "review", Target: "prompts",
				Verdict:        "confounded",
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 8, Eligible: 8},
				AfterCoverage:  cadence.Coverage{Total: 6, Eligible: 6},
				Confounds: []mine.Confound{{Category: "instrument", Reason: "after-window reviewed by another instrument",
					Before: []string{"agy@sha"}, After: []string{"agy@other"}}},
			}, {
				ID: "lost-ledger", Class: "lost ledger", Tier: "guard", Target: "guard.py",
				Verdict:        "evidence-incomplete",
				Scope:          &cadence.Scope{Version: 1, All: true},
				BeforeCoverage: cadence.Coverage{Total: 5, Eligible: 5},
				AfterCoverage:  cadence.Coverage{Total: 4, Unknown: 4, UnknownEvidence: 4},
			}},
			Unscored:   []string{"taste-rule"},
			Candidates: []cadence.Candidate{{Corpus: "transcript-friction", Pattern: "use pnpm", Count: 7, Sessions: 4}},
		},
		Telemetry: Telemetry{ReviewChains: 24, DenialsRows: 5, AuditFirings: 47},
		Corpora: []CorpusRow{
			{Corpus: "review-findings", Present: true, Size: 24, Path: "/state/review"},
			{Corpus: "auto-denials", Present: false},
		},
	}
}

func render(t *testing.T, s *SystemStatus) string {
	t.Helper()
	var buf bytes.Buffer
	if err := s.RenderHTML(&buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	return buf.String()
}

func TestRenderHTMLCarriesEverySection(t *testing.T) {
	out := render(t, sample())

	for _, want := range []string{
		"<!DOCTYPE html>", "steer", "Attention required", "Failure-class ledger",
		"Active interventions by tier", "Recent review chains", "System health",
		"widget flag loss", "bare python call", "naming taste",
		"use pnpm", "taste-rule",
		"v-effective", "v-not-triggered", "v-confounded", "v-evidence-incomplete",
		"1 of 2 present", "agy@sha",
		// The measurement conditions the verdict rests on, one target at a time.
		"RR 0.00 [0.00, 0.31] @90%", "needs 9 clean eligible chain(s)",
		"scope repo=/steer.*/ AND path=/\\.go$/", "scope all",
		"12 eligible / 14 (2 out of scope)", "12 eligible / 13 (1 pending)",
		"0 eligible / 4 (4 without a ledger)",
		"confound (instrument): after-window reviewed by another instrument",
		"composition (host): host set changed",
		// Resolver posture: the arming row and the unresolved backlog.
		"resolver claude-code global", "4 row(s) owe a ledger",
		// Posture is harness-qualified, and Codex's trust is a record, not a proof.
		"gate codex global", "trust record present, unverified",
		// The session corpus's own accounting and the guard's audit join.
		"20 eligible / 22 (2 partly exposed)",
		"attribution: no-bare-python never fired in the after-window",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	// No runtime dependency: the page loads nothing over the network.
	for _, bad := range []string{"<script", "http://", "https://"} {
		if strings.Contains(out, bad) {
			t.Errorf("the export must be self-contained, found %q", bad)
		}
	}
}

func TestRenderHTMLEmptyMachine(t *testing.T) {
	s := &SystemStatus{
		StatusVersion: StatusVersion,
		Summary:       VerdictSummary{Counts: map[string]int{}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
		},
	}
	out := render(t, s)
	for _, want := range []string{
		"the loop is quiet",
		"No interventions recorded on this machine.",
		"No active interventions installed.",
		"No adjudicated chains on this machine.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("empty page is missing %q", want)
		}
	}
}

// TestRenderHTMLIsDeterministic guards the map iteration inside the groupers: two
// renders of one snapshot must agree byte for byte.
func TestRenderHTMLIsDeterministic(t *testing.T) {
	s := sample()
	if a, b := render(t, s), render(t, s); a != b {
		t.Error("two renders of one snapshot differ")
	}
}

// TestRenderHTMLEscapesClassText pins that a class name carrying markup is escaped,
// never injected — class text comes from a model-authored ledger row.
func TestRenderHTMLEscapesClassText(t *testing.T) {
	s := sample()
	s.Interventions[0].Class = `<img src=x onerror="alert(1)">`
	out := render(t, s)
	if strings.Contains(out, "<img src=x") {
		t.Error("class text must be escaped, not injected")
	}
	if !strings.Contains(out, "&lt;img") {
		t.Error("escaped class text should still appear in the page")
	}
}

func TestDeltaSignsTheRateMovement(t *testing.T) {
	cases := []struct {
		before, after float64
		want          string
	}{
		{1, 0, `class="d-down">-100.0%`},
		{0.1, 0.25, `class="d-up">+15.0%`},
		{0.2, 0.2, `class="dimtext">0.0%`},
	}
	for _, c := range cases {
		if got := string(delta(c.before, c.after)); !strings.Contains(got, c.want) {
			t.Errorf("delta(%v, %v) = %q, want it to contain %q", c.before, c.after, got, c.want)
		}
	}
}

func TestTierGroupsAreLadderOrderedAndActiveOnly(t *testing.T) {
	items := []ScoredIntervention{
		{Class: "d", Tier: "review", Status: "active"},
		{Class: "a", Tier: "check", Status: "active"},
		{Class: "z", Tier: "novel", Status: "active"},
		{Class: "b", Tier: "guard", Status: "active"},
		{Class: "gone", Tier: "check", Status: "rejected"},
	}
	got := tierGroups(items)
	want := []string{"check", "guard", "review", "novel"}
	if len(got) != len(want) {
		t.Fatalf("want %d tier groups, got %d", len(want), len(got))
	}
	for i, key := range want {
		if got[i].Key != key {
			t.Errorf("group %d: got %q, want %q", i, got[i].Key, key)
		}
		if len(got[i].Items) != 1 {
			t.Errorf("group %q should hold the one active row, got %d", key, len(got[i].Items))
		}
	}
}

func TestLedgerSortsUrgentVerdictsFirst(t *testing.T) {
	got := sortedLedger([]ScoredIntervention{
		{Class: "a", Verdict: "effective"},
		{Class: "b", Verdict: "ineffective"},
		{Class: "c", Verdict: "collecting"},
		{Class: "d", Verdict: "confounded"},
		{Class: "e", Verdict: "evidence-incomplete"},
		{Class: "d", Verdict: "harmful"},
	})
	want := []string{"harmful", "ineffective", "evidence-incomplete", "confounded",
		"collecting", "effective"}
	for i, v := range want {
		if got[i].Verdict != v {
			t.Errorf("row %d: got %q, want %q", i, got[i].Verdict, v)
		}
	}
}
