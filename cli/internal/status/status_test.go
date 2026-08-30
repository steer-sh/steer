package status

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// isolate points HOME at a temp dir so the denials read, the audit-log count, and
// the posture readers all resolve inside the sandbox, never the real machine.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// seedLedger writes a ledger of `n` before-install chains carrying beforeText and
// `n` after-install chains carrying afterText, plus one intervention record for a
// class matching beforeText, installed between the two windows.
func seedLedger(t *testing.T, class, match, beforeText, afterText string, n int) string {
	t.Helper()
	stateDir := t.TempDir()
	var rows []gate.Row
	ts := 1000
	push := func(text string) {
		rows = append(rows,
			gate.Row{"ts": ts, "phase": "deny", "firing": 1, "files": 1, "host": "h"},
			gate.Row{"ts": ts + 1, "phase": "retry", "firing": 1, "files": 1, "host": "h",
				"ledger_join": "round", "session_id": "s1",
				"concerns": []any{map[string]any{"concern": text, "verdict": "ACCEPTED"}}},
			gate.Row{"ts": ts + 2, "phase": "clean", "firing": 2, "files": 1, "host": "h"})
		ts += 100
	}
	for i := 0; i < n; i++ {
		push(beforeText)
	}
	installTS := ts
	for i := 0; i < n; i++ {
		push(afterText)
	}

	ledger := filepath.Join(stateDir, "adjudication.h.jsonl")
	f, err := os.Create(ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		data, _ := json.Marshal(r)
		f.Write(append(data, '\n'))
	}
	f.Close()

	rec := mine.Record{
		"id": "widget-flag", "class": class, "match": match,
		"kind": "defect", "tier": "check", "target": "check.sh",
		"status": "active", "unit": mine.UnitChain, "corpus": mine.CorpusReviewFindings,
		"installed_ts": installTS,
	}
	if err := mine.Save(stateDir, []mine.Record{rec}); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

func TestCollectFullInventoryAndTelemetry(t *testing.T) {
	isolate(t)
	stateDir := seedLedger(t, "widget parser flag loss", "widget parser",
		"widget parser drops a flag", "unrelated naming nit", 12)

	s, err := Collect(Options{GateStateDir: stateDir, WindowDays: 7})
	if err != nil {
		t.Fatal(err)
	}

	if s.StatusVersion != StatusVersion {
		t.Errorf("status version: got %d, want %d", s.StatusVersion, StatusVersion)
	}
	if s.Summary.Total != 1 {
		t.Fatalf("want 1 live intervention, got %d", s.Summary.Total)
	}
	if len(s.Interventions) != 1 {
		t.Fatalf("want 1 inventory row, got %d", len(s.Interventions))
	}
	iv := s.Interventions[0]
	// The class was eliminated after install: its after-window carries a different
	// finding, so the verdict is a decided one, never insufficient-data.
	if iv.Verdict == "" || iv.Verdict == "insufficient-data" {
		t.Errorf("class should score a decided verdict, got %q", iv.Verdict)
	}
	if iv.Before.Hits == 0 {
		t.Error("the before window should carry the class")
	}
	if iv.After.Hits != 0 {
		t.Errorf("the after window eliminated the class, got %d hits", iv.After.Hits)
	}
	if s.Summary.Counts[iv.Verdict] != 1 {
		t.Errorf("summary should count the verdict once, got %+v", s.Summary.Counts)
	}

	// Telemetry: 24 chains total (12 before + 12 after), no denials, no audit log.
	if s.Telemetry.ReviewChains != 24 {
		t.Errorf("want 24 review chains, got %d", s.Telemetry.ReviewChains)
	}
	if s.Telemetry.DenialsRows != 0 || s.Telemetry.AuditFirings != 0 {
		t.Errorf("an empty machine should report zero denials and firings, got %+v", s.Telemetry)
	}
	if len(s.Chains) == 0 {
		t.Error("recent chains should not be empty")
	}
	// One corpus row per corpus, from the cadence sweep.
	if len(s.Corpora) != 3 {
		t.Errorf("want 3 corpus rows from the sweep, got %d", len(s.Corpora))
	}
}

func TestCollectEmptyMachineFailsSoft(t *testing.T) {
	isolate(t)
	s, err := Collect(Options{GateStateDir: t.TempDir(), WindowDays: 7})
	if err != nil {
		t.Fatalf("an empty machine must not error: %v", err)
	}
	if s.Summary.Total != 0 || len(s.Interventions) != 0 {
		t.Errorf("empty machine should carry no interventions, got %+v", s.Summary)
	}
	if len(s.Attention.Interventions) != 0 || len(s.Attention.Candidates) != 0 {
		t.Error("empty machine should carry an empty attention queue")
	}
	// Posture reads still populate both scopes, all not armed.
	if len(s.Posture.CommitGate) != 2 || len(s.Posture.Denials) != 2 {
		t.Errorf("posture should report both scopes, got %+v", s.Posture)
	}
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(n int) *int           { return &n }

// TestJSONContractIsStable pins the emitted key set and order. It marshals a fully
// populated SystemStatus literal — no Config, no machine state — against a golden
// so a renamed or reordered json tag fails here, the agent-contract guard.
func TestJSONContractIsStable(t *testing.T) {
	s := &SystemStatus{
		StatusVersion: 3,
		GeneratedAt:   "2026-01-01T00:00:00Z",
		Posture: Posture{
			Instrument: "agy@sha", Reviewer: "gemini", Ledger: "/l.jsonl",
			CommitGate: []ScopeArming{{Scope: "global", Registered: true, Effective: true, Timeout: 90}},
			Denials:    []ScopeArming{{Scope: "global", Registered: true, Effective: true}},
			Resolver:   []ScopeArming{{Scope: "global", Registered: true, Effective: true, Timeout: 30}},
			Unresolved: 4,
		},
		Interventions: []ScoredIntervention{{
			ID: "widget-flag", Class: "widget flag loss", Match: "widget.*flag", Tier: "guard", Target: "guard.py",
			Status: "active", Verdict: "effective",
			Before: cadence.Incidence{Hits: 12, Units: 12, Rate: 1}, After: cadence.Incidence{Hits: 0, Units: 12, Rate: 0},
			PValue: 0.02, InstrumentDrift: true, FiringsApplicable: true,
			Scope:          &cadence.Scope{Version: 1, Repo: "steer.*", Path: `\.go$`},
			BeforeCoverage: cadence.Coverage{Total: 14, Eligible: 12, Ineligible: 1, Unknown: 1, UnknownScope: 1},
			AfterCoverage:  cadence.Coverage{Total: 13, Eligible: 12, Unknown: 1, UnknownEvidence: 1, Pending: 1},
			Effect: cadence.Effect{
				Estimate: floatPtr(0), Lower: 0, Upper: floatPtr(0.31),
				Confidence: 0.9, Method: "clopper-pearson / mixture-cs",
			},
			RequiredAfter: intPtr(9),
			Confounds:     []mine.Confound{},
			Composition:   []mine.Confound{{Category: "host", Reason: "host set changed", Before: []string{"a"}, After: []string{"a", "b"}}},
		}, {
			ID: "taste-nit", Class: "taste nit", Match: "nit", Tier: "check", Target: "check.sh",
			Status: "rejected", Verdict: "class-steady",
			Reason: "not mechanizable", Reopen: "incidence doubles", RejectedTS: 1700,
			Scope:       &cadence.Scope{Version: 1, All: true},
			Confounds:   []mine.Confound{},
			Composition: []mine.Confound{},
		}},
		Summary: VerdictSummary{Total: 2, Counts: map[string]int{"effective": 1, "class-steady": 1}},
		Chains: []ChainSummary{{
			Host: "h", StartTS: 1000, Rounds: 2, Files: 1, Findings: 1, Accepted: 1, Sessions: 1, Terminal: "clean",
		}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
		},
		Telemetry: Telemetry{ReviewChains: 24},
		Corpora:   []CorpusRow{{Corpus: "review-findings", Present: true, Size: 24, Path: "/state/review"}},
		Hooks:     []HookInfo{{Name: "no-console", Scope: "global", Source: "generated", Rule: "no console.log"}},
		Presets:   []PresetInfo{{Name: "shell-safety", Bundle: "security", Description: "block risky shell", Enabled: true, Drifted: false}},
		Team:      TeamState{Present: true, Path: "steer-team.lock.json", HooksLocked: 3, CatalogVersion: "v7"},
	}
	got, err := s.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), []byte(goldenStatusJSON)) {
		t.Errorf("JSON contract drifted.\n--- got ---\n%s\n--- want ---\n%s", got, goldenStatusJSON)
	}
}

const goldenStatusJSON = `{
  "status_version": 3,
  "generated_at": "2026-01-01T00:00:00Z",
  "posture": {
    "instrument": "agy@sha",
    "reviewer": "gemini",
    "ledger": "/l.jsonl",
    "commit_gate": [
      {
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 90
      }
    ],
    "denials": [
      {
        "scope": "global",
        "registered": true,
        "effective": true
      }
    ],
    "resolver": [
      {
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 30
      }
    ],
    "unresolved_rows": 4
  },
  "interventions": [
    {
      "id": "widget-flag",
      "class": "widget flag loss",
      "match": "widget.*flag",
      "tier": "guard",
      "target": "guard.py",
      "status": "active",
      "verdict": "effective",
      "before": {
        "hits": 12,
        "units": 12,
        "rate": 1
      },
      "after": {
        "hits": 0,
        "units": 12,
        "rate": 0
      },
      "p_value": 0.02,
      "instrument_drift": true,
      "firings_applicable": true,
      "scope": {
        "version": 1,
        "repo": "steer.*",
        "path": "\\.go$"
      },
      "before_coverage": {
        "total": 14,
        "eligible": 12,
        "ineligible": 1,
        "unknown": 1,
        "unknown_scope": 1,
        "unknown_evidence": 0,
        "pending": 0
      },
      "after_coverage": {
        "total": 13,
        "eligible": 12,
        "ineligible": 0,
        "unknown": 1,
        "unknown_scope": 0,
        "unknown_evidence": 1,
        "pending": 1
      },
      "effect": {
        "estimate": 0,
        "lower": 0,
        "upper": 0.31,
        "confidence": 0.9,
        "method": "clopper-pearson / mixture-cs"
      },
      "required_after": 9,
      "confounds": [],
      "composition": [
        {
          "category": "host",
          "reason": "host set changed",
          "before": [
            "a"
          ],
          "after": [
            "a",
            "b"
          ]
        }
      ]
    },
    {
      "id": "taste-nit",
      "class": "taste nit",
      "match": "nit",
      "tier": "check",
      "target": "check.sh",
      "status": "rejected",
      "verdict": "class-steady",
      "before": {
        "hits": 0,
        "units": 0,
        "rate": 0
      },
      "after": {
        "hits": 0,
        "units": 0,
        "rate": 0
      },
      "firings_applicable": false,
      "reason": "not mechanizable",
      "reopen": "incidence doubles",
      "rejected_ts": 1700,
      "scope": {
        "version": 1,
        "all": true
      },
      "before_coverage": {
        "total": 0,
        "eligible": 0,
        "ineligible": 0,
        "unknown": 0,
        "unknown_scope": 0,
        "unknown_evidence": 0,
        "pending": 0
      },
      "after_coverage": {
        "total": 0,
        "eligible": 0,
        "ineligible": 0,
        "unknown": 0,
        "unknown_scope": 0,
        "unknown_evidence": 0,
        "pending": 0
      },
      "effect": {
        "estimate": null,
        "lower": 0,
        "upper": null,
        "confidence": 0,
        "method": ""
      },
      "required_after": null,
      "confounds": [],
      "composition": []
    }
  ],
  "summary": {
    "total": 2,
    "counts": {
      "class-steady": 1,
      "effective": 1
    }
  },
  "chains": [
    {
      "host": "h",
      "start_ts": 1000,
      "rounds": 2,
      "files": 1,
      "findings": 1,
      "accepted": 1,
      "rejected": 0,
      "sessions": 1,
      "terminal": "clean"
    }
  ],
  "attention": {
    "interventions": [],
    "unscored": [],
    "candidates": []
  },
  "telemetry": {
    "review_chains": 24,
    "denials_rows": 0,
    "audit_firings": 0
  },
  "corpora": [
    {
      "corpus": "review-findings",
      "present": true,
      "size": 24,
      "path": "/state/review"
    }
  ],
  "hooks": [
    {
      "name": "no-console",
      "scope": "global",
      "source": "generated",
      "rule": "no console.log"
    }
  ],
  "presets": [
    {
      "name": "shell-safety",
      "bundle": "security",
      "description": "block risky shell",
      "enabled": true,
      "drifted": false
    }
  ],
  "team": {
    "present": true,
    "path": "steer-team.lock.json",
    "hooks_locked": 3,
    "catalog_version": "v7"
  }
}`
