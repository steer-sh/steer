package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// sample is a populated snapshot covering every branch the page renders: a live
// effective class, a flagged one, a candidate, an unscored record and a chain.
func sample() *SystemStatus {
	return &SystemStatus{
		StatusVersion: StatusVersion,
		GeneratedAt:   "2026-01-01T00:00:00Z",
		Posture: Posture{
			Instrument: "agy@sha", Reviewer: "gemini", Ledger: "/l.jsonl",
			CommitGate: []ScopeArming{
				{Scope: "project", Registered: false},
				{Scope: "global", Registered: true, Effective: true, Timeout: 90},
			},
			Denials: []ScopeArming{{Scope: "global", Registered: true}},
		},
		Interventions: []ScoredIntervention{
			{ID: "widget-flag", Class: "widget flag loss", Tier: "check", Target: "check.sh",
				Status: "active", HookID: "h1", Verdict: "effective",
				Before: cadence.Incidence{Hits: 12, Units: 12, Rate: 1},
				After:  cadence.Incidence{Hits: 0, Units: 12, Rate: 0}},
			{ID: "bare-python", Class: "bare python call", Tier: "guard", Target: "steer",
				Status: "active", Verdict: "ineffective",
				Before: cadence.Incidence{Hits: 2, Units: 20, Rate: 0.1},
				After:  cadence.Incidence{Hits: 5, Units: 20, Rate: 0.25}},
			{ID: "taste-rule", Class: "naming taste", Tier: "instruction", Target: "CLAUDE.md",
				Status: "rejected", Verdict: "unscored"},
		},
		Summary: VerdictSummary{Total: 3, Counts: map[string]int{
			"effective": 1, "ineffective": 1, "unscored": 1}},
		Chains: []ChainSummary{{Host: "h", StartTS: 1700000000, Rounds: 2, Files: 3,
			Findings: 2, Accepted: 1, Rejected: 1, Sessions: 1, Terminal: "clean"}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{{
				ID: "bare-python", Class: "bare python call", Tier: "guard", Target: "steer",
				Verdict: "ineffective",
				Before:  cadence.Incidence{Hits: 2, Units: 20, Rate: 0.1},
				After:   cadence.Incidence{Hits: 5, Units: 20, Rate: 0.25}}},
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
		"v-effective", "v-ineffective",
		"1 of 2 present", "agy@sha",
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
		{Class: "d", Verdict: "harmful"},
	})
	want := []string{"harmful", "ineffective", "collecting", "effective"}
	for i, v := range want {
		if got[i].Verdict != v {
			t.Errorf("row %d: got %q, want %q", i, got[i].Verdict, v)
		}
	}
}
