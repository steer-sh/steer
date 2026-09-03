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

// TestPanelsCarryTheMeasurementConditions pins that the panels name what the verdict
// rests on: the effect range, the frozen scope, each window's coverage, and the
// resolver posture behind the evidence.
func TestPanelsCarryTheMeasurementConditions(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{{Scope: "global", Registered: true, Effective: true}},
			Denials:    []status.ScopeArming{{Scope: "global"}},
			Resolver:   []status.ScopeArming{{Scope: "global", Registered: true, Effective: true, Timeout: 30}},
			Unresolved: 4,
		},
		Interventions: []status.ScoredIntervention{{
			ID: "bare-python", Class: "bare python call", Match: "python ", Tier: "guard",
			Target: "guard.py", Status: "active", Verdict: "confounded",
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

	m.active = panelClasses
	classes := m.View()
	for _, want := range []string{
		"confounded", "a clean window of 9 units",
		// No range under a withheld verdict: the action sentence carries the row.
		"the measurement conditions changed mid-window",
		"scope repo=/steer/ AND path=/\\.go$/",
		"before 12 eligible / 14 (2 out of scope)",
		"after 5 eligible / 6 (1 without a ledger)",
		"confound (instrument): after-window reviewed by another instrument",
	} {
		if !strings.Contains(classes, want) {
			t.Errorf("the classes panel is missing %q", want)
		}
	}

	m.active = panelChannels
	channels := m.View()
	for _, want := range []string{"resolver", "4 ledger row(s) owe a transcript join"} {
		if !strings.Contains(channels, want) {
			t.Errorf("the channels panel is missing %q", want)
		}
	}

	m.active = panelHealth
	if !strings.Contains(m.View(), "the scorer withholds on them") {
		t.Error("the health panel does not name the unresolved backlog")
	}
}

func intPtr(n int) *int { return &n }
