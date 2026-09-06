package tui

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestModelRendersEveryPanel checks that each panel renders a non-empty frame
// over a default (empty-machine) snapshot, so no panel view panics or blanks.
func TestModelRendersEveryPanel(t *testing.T) {
	tempSettingsHome(t)
	m := New(gate.Defaults(), "global")
	for p := panelChannels; p < panelCount; p++ {
		m.active = p
		if got := m.View(); got == "" {
			t.Fatalf("panel %d rendered an empty frame", p)
		}
	}
}

// TestPanelsCarryTheMeasurementConditions pins that the panels name what a result
// rests on: the compact summary carries the result and why it is withheld, the
// expanded entry carries each period's counts and what changed between them, and
// the technical block carries the eligibility rule and the window still needed.
func TestPanelsCarryTheMeasurementConditions(t *testing.T) {
	m := New(gate.Defaults(), "global")
	// A wide terminal, so an assertion reads one unwrapped line. Wrapping has its
	// own tests.
	m.width = 200
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{{Scope: "global", Registered: true, Effective: true}},
			Denials:    []status.ScopeArming{{Scope: "global"}},
			Resolver:   []status.ScopeArming{{Scope: "global", Registered: true, Effective: true, Timeout: 30}},
			Unresolved: 4,
		},
		Interventions: []status.ScoredIntervention{{
			ID: "bare-python", Class: "bare python call", Match: "python ", Tier: "guard",
			Target: "guard.py", Status: "active", Verdict: "confounded", Unit: "chain",
			Scope:          &cadence.Scope{Version: 1, Repo: "steer", Path: `\.go$`},
			BeforeCoverage: cadence.Coverage{Total: 14, Eligible: 12, Ineligible: 2},
			AfterCoverage:  cadence.Coverage{Total: 6, Eligible: 5, Unknown: 1, UnknownEvidence: 1},
			Effect: cadence.Effect{Lower: 0.2, Confidence: 0.9,
				Method: "clopper-pearson / mixture-cs"},
			RequiredAfter: intPtr(9),
			Confounds: []mine.Confound{{Category: "instrument",
				Reason: "after-window reviewed by another instrument"}},
			Composition: []mine.Confound{},
		}},
	}

	m.active = panelProblems
	summary := m.View()
	for _, want := range []string{
		"Not comparable",
		// No range under a withheld result: the reason carries the row.
		"these two periods ran under different conditions and cannot be compared",
		"how the evidence was produced",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the compact summary is missing %q", want)
		}
	}
	if strings.Contains(summary, "RR ") {
		t.Error("a withheld result printed a range")
	}

	m.updateNav(keyOf("enter"))
	m.panelKey(keyOf("t"))
	expanded := m.View()
	for _, want := range []string{
		"12 eligible / 14 (2 out of scope)",
		"5 eligible / 6 (1 with no decision recorded)",
		"how the evidence was produced — after-window reviewed by another instrument",
		"repo=/steer/ AND path=/\\.go$/",
		"needs 9 more clean, eligible review sequences",
	} {
		if !strings.Contains(expanded, want) {
			t.Errorf("the expanded entry is missing %q:\n%s", want, expanded)
		}
	}

	m.active = panelChannels
	channels := m.View()
	if !strings.Contains(channels, "read 4 review outcomes from your session history") {
		t.Error("the arming panel does not offer to read the waiting outcomes")
	}

	m.active = panelDiagnostics
	if !strings.Contains(m.View(), "4 reviews have no outcome yet") {
		t.Error("diagnostics does not name the reviews still waiting")
	}
}

func intPtr(n int) *int { return &n }
