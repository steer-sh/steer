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

// TestJSONContractIsStable pins the emitted key set and order. It marshals a fully
// populated SystemStatus literal — no Config, no machine state — against a golden
// so a renamed or reordered json tag fails here, the agent-contract guard.
func TestJSONContractIsStable(t *testing.T) {
	s := &SystemStatus{
		StatusVersion: 1,
		GeneratedAt:   "2026-01-01T00:00:00Z",
		Posture: Posture{
			Instrument: "agy@sha", Reviewer: "gemini", Ledger: "/l.jsonl",
			CommitGate: []ScopeArming{{Scope: "global", Registered: true, Effective: true, Timeout: 90}},
			Denials:    []ScopeArming{{Scope: "global", Registered: true, Effective: true}},
		},
		Interventions: []ScoredIntervention{{
			ID: "widget-flag", Class: "widget flag loss", Tier: "check", Target: "check.sh",
			Status: "active", Verdict: "effective",
			Before: cadence.Incidence{Hits: 12, Units: 12, Rate: 1}, After: cadence.Incidence{Hits: 0, Units: 12, Rate: 0},
		}},
		Summary: VerdictSummary{Total: 1, Counts: map[string]int{"effective": 1}},
		Chains: []ChainSummary{{
			Host: "h", StartTS: 1000, Rounds: 2, Files: 1, Findings: 1, Accepted: 1, Sessions: 1, Terminal: "clean",
		}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
		},
		Telemetry: Telemetry{ReviewChains: 24},
		Corpora:   []cadence.CorpusStatus{{Corpus: "review-findings", Present: true, Size: 24}},
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
  "status_version": 1,
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
    ]
  },
  "interventions": [
    {
      "id": "widget-flag",
      "class": "widget flag loss",
      "tier": "check",
      "target": "check.sh",
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
      }
    }
  ],
  "summary": {
    "total": 1,
    "counts": {
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
      "size": 24
    }
  ]
}`
