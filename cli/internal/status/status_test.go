package status

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
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
	// Posture is harness-qualified: the commit gate and its resolver report both
	// harnesses in both scopes, denials only Claude Code, and nothing is armed.
	if len(s.Posture.CommitGate) != 4 || len(s.Posture.Resolver) != 4 {
		t.Errorf("gate posture should report both harnesses in both scopes, got %+v", s.Posture)
	}
	if len(s.Posture.Denials) != 2 {
		t.Errorf("denials is Claude Code's alone, got %+v", s.Posture.Denials)
	}
	for _, row := range append(append([]ScopeArming{}, s.Posture.CommitGate...), s.Posture.Denials...) {
		if row.Harness == "" {
			t.Errorf("every posture row names its harness, got %+v", row)
		}
		if row.Registered || row.Effective {
			t.Errorf("an empty machine arms nothing, got %+v", row)
		}
	}
	// Codex reviews hook trust itself, so registration alone proves nothing there.
	for _, row := range s.Posture.CommitGate {
		want := TrustNotApplicable
		if row.Harness == "codex" {
			want = TrustUnknown
		}
		if row.Trust != want {
			t.Errorf("%s trust: got %q, want %q", row.Harness, row.Trust, want)
		}
	}
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(n int) *int           { return &n }

// TestJSONContractIsStable pins the emitted key set and order. It marshals a fully
// populated SystemStatus literal — no Config, no machine state — against a golden
// so a renamed or reordered json tag fails here, the agent-contract guard.
func TestJSONContractIsStable(t *testing.T) {
	s := &SystemStatus{
		StatusVersion: 6,
		GeneratedAt:   "2026-01-01T00:00:00Z",
		Posture: Posture{
			Instrument: "agy@sha", Reviewer: "gemini", Ledger: "/l.jsonl",
			CommitGate: []ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true, Timeout: 90,
					Trust: TrustNotApplicable, Command: "/h/.steer/bin/steer commit-gate review",
					Resolvable: ResolveResolvable, Observed: 1700, ObservedCorpus: ledgerCorpus},
				{Harness: "codex", Scope: "global", Registered: true, Effective: true, Timeout: 90,
					Trust: TrustRecordPresent, Command: "steer commit-gate review-codex",
					Resolvable: ResolveMissing, ObservedCorpus: ledgerCorpus},
			},
			Denials: []ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true,
					Trust: TrustNotApplicable, Command: "/h/.steer/bin/steer denials record",
					Resolvable: ResolveResolvable, Observed: 1650, ObservedCorpus: denialsCorpus},
			},
			Resolver: []ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true, Timeout: 30,
					Trust: TrustNotApplicable, Command: "/h/.steer/bin/steer commit-gate backfill",
					Resolvable: ResolveResolvable},
				{Harness: "codex", Scope: "global", Registered: false, Effective: false,
					Trust: TrustMissing, Resolvable: ResolveNotRegistered},
			},
			Masters: []MasterRuntime{
				{Harness: "claude-code", Scope: "global", Path: "/h/steer-hooks.py",
					Runtime: "aaaaaaaa", Version: "0.1.0", Expected: "bbbbbbbb", State: MasterStale, Observed: 1690},
				{Harness: "codex", Scope: "global", Expected: "bbbbbbbb", State: MasterNotRegistered},
			},
			Unresolved: 4,
		},
		Interventions: []ScoredIntervention{{
			ID: "widget-flag", Class: "widget flag loss", Match: "widget.*flag", Tier: "guard", Target: "guard.py",
			Status: "active", HookID: "no-widget-flag", Verdict: "effective",
			Corpus: mine.CorpusReviewFindings, Unit: mine.UnitChain,
			Attribution: cadence.Attribution{
				Applicable: true, LogPresent: true, HookID: "no-widget-flag", Firings: 3, Sessions: 2,
			},
			Before: cadence.Incidence{Hits: 12, Units: 12, Rate: 1}, After: cadence.Incidence{Hits: 0, Units: 12, Rate: 0},
			PValue: 0.02, InstrumentDrift: true, FiringsApplicable: true,
			Scope:          &cadence.Scope{Version: 1, Repo: "steer.*", Path: `\.go$`},
			BeforeCoverage: cadence.Coverage{Total: 14, Eligible: 12, Ineligible: 1, Unknown: 1, UnknownScope: 1},
			AfterCoverage:  cadence.Coverage{Total: 13, Eligible: 12, Unknown: 1, UnknownEvidence: 1, Pending: 1, PartialExposure: 1},
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
			Corpus: mine.CorpusTranscript, Unit: mine.UnitSession, SignalKind: mine.SignalCorrection,
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
		Telemetry: Telemetry{
			ReviewChains: 24, DenialsRows: 2, AuditFirings: 3, SessionSnapshots: 40,
			Harnesses: []HarnessTelemetry{
				{Harness: "claude-code", Sessions: 30, Firings: 3, Chains: 24},
				{Harness: "codex", Sessions: 10},
			},
		},
		Corpora: []CorpusRow{
			{Corpus: "review-findings", Present: true, Size: 24, Path: "/state/review"},
			{Corpus: "transcript-friction", Present: true, Size: 40, Path: "/state/sessions"},
		},
		Hooks:   []HookInfo{{Name: "no-console", Scope: "global", Source: "generated", Rule: "no console.log"}},
		Presets: []PresetInfo{{Name: "shell-safety", Bundle: "security", Description: "block risky shell", Enabled: true, Drifted: false}},
		Team:    TeamState{Present: true, Path: "steer-team.lock.json", HooksLocked: 3, CatalogVersion: "v7"},
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
  "status_version": 6,
  "generated_at": "2026-01-01T00:00:00Z",
  "posture": {
    "instrument": "agy@sha",
    "reviewer": "gemini",
    "ledger": "/l.jsonl",
    "commit_gate": [
      {
        "harness": "claude-code",
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 90,
        "trust": "not-applicable",
        "command": "/h/.steer/bin/steer commit-gate review",
        "resolvable": "resolvable",
        "observed": 1700,
        "observed_corpus": "commit-review"
      },
      {
        "harness": "codex",
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 90,
        "trust": "record-present-unverified",
        "command": "steer commit-gate review-codex",
        "resolvable": "missing",
        "observed_corpus": "commit-review"
      }
    ],
    "denials": [
      {
        "harness": "claude-code",
        "scope": "global",
        "registered": true,
        "effective": true,
        "trust": "not-applicable",
        "command": "/h/.steer/bin/steer denials record",
        "resolvable": "resolvable",
        "observed": 1650,
        "observed_corpus": "steer-denials"
      }
    ],
    "resolver": [
      {
        "harness": "claude-code",
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 30,
        "trust": "not-applicable",
        "command": "/h/.steer/bin/steer commit-gate backfill",
        "resolvable": "resolvable"
      },
      {
        "harness": "codex",
        "scope": "global",
        "registered": false,
        "effective": false,
        "trust": "missing",
        "resolvable": "not-registered"
      }
    ],
    "unresolved_rows": 4,
    "masters": [
      {
        "harness": "claude-code",
        "scope": "global",
        "path": "/h/steer-hooks.py",
        "runtime": "aaaaaaaa",
        "version": "0.1.0",
        "expected": "bbbbbbbb",
        "state": "stale",
        "observed": 1690
      },
      {
        "harness": "codex",
        "scope": "global",
        "expected": "bbbbbbbb",
        "state": "not-registered"
      }
    ]
  },
  "interventions": [
    {
      "id": "widget-flag",
      "class": "widget flag loss",
      "match": "widget.*flag",
      "tier": "guard",
      "target": "guard.py",
      "status": "active",
      "hook_id": "no-widget-flag",
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
        "pending": 0,
        "partial_exposure": 0
      },
      "after_coverage": {
        "total": 13,
        "eligible": 12,
        "ineligible": 0,
        "unknown": 1,
        "unknown_scope": 0,
        "unknown_evidence": 1,
        "pending": 1,
        "partial_exposure": 1
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
      ],
      "corpus": "review-findings",
      "unit": "chain",
      "attribution": {
        "applicable": true,
        "log_present": true,
        "hook_id": "no-widget-flag",
        "firings": 3,
        "sessions": 2
      }
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
        "pending": 0,
        "partial_exposure": 0
      },
      "after_coverage": {
        "total": 0,
        "eligible": 0,
        "ineligible": 0,
        "unknown": 0,
        "unknown_scope": 0,
        "unknown_evidence": 0,
        "pending": 0,
        "partial_exposure": 0
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
      "composition": [],
      "corpus": "transcript-friction",
      "unit": "session",
      "signal_kind": "user_corrections",
      "attribution": {
        "applicable": false,
        "log_present": false,
        "firings": 0,
        "sessions": 0
      }
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
    "denials_rows": 2,
    "audit_firings": 3,
    "session_snapshots": 40,
    "harnesses": [
      {
        "harness": "claude-code",
        "sessions": 30,
        "firings": 3,
        "chains": 24
      },
      {
        "harness": "codex",
        "sessions": 10,
        "firings": 0,
        "chains": 0
      }
    ]
  },
  "corpora": [
    {
      "corpus": "review-findings",
      "present": true,
      "size": 24,
      "path": "/state/review"
    },
    {
      "corpus": "transcript-friction",
      "present": true,
      "size": 40,
      "path": "/state/sessions"
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

// TestABrokenRegistrationIsNotArmed pins the reading item 14 measured: a gate
// whose command does not resolve reads as registered and effective from the
// configuration alone, so the posture has to carry the second reading. It also
// pins the third: a channel that never wrote a row reports never observed.
func TestABrokenRegistrationIsNotArmed(t *testing.T) {
	isolate(t)
	if err := claude.RegisterCommitGateHook(claude.GlobalScope,
		"steer-not-real-xyz commit-gate review", 210); err != nil {
		t.Fatal(err)
	}

	p := collectPosture(gate.Config{}, mine.EvidenceSet{})

	var row ScopeArming
	for _, a := range p.CommitGate {
		if a.Harness == "claude-code" && a.Scope == claude.GlobalScope {
			row = a
		}
	}
	if !row.Registered || !row.Effective {
		t.Fatalf("the configuration reads as %+v; the registration is present", row)
	}
	if row.Resolvable != ResolveMissing {
		t.Fatalf("resolvable = %q, want %q", row.Resolvable, ResolveMissing)
	}
	if row.Command != "steer-not-real-xyz commit-gate review" {
		t.Fatalf("command = %q, want the registered one verbatim", row.Command)
	}
	if got := ResolvePhrase(row); got == "" {
		t.Fatal("a missing command renders no warning")
	}
	if row.ObservedCorpus != ledgerCorpus || row.Observed != 0 {
		t.Fatalf("observed = %v/%q, want never observed over the ledger", row.Observed, row.ObservedCorpus)
	}
	if got := ObservedPhrase(row); got != "never observed" {
		t.Fatalf("observed phrase = %q, want %q", got, "never observed")
	}
}

// TestAnUnregisteredChannelResolvesToNothing: a scope with no registration has no
// command to test, so it reports not-registered rather than missing — a missing
// command is a broken install, and an absent one is not installed.
func TestAnUnregisteredChannelResolvesToNothing(t *testing.T) {
	isolate(t)

	p := collectPosture(gate.Config{}, mine.EvidenceSet{})

	for _, a := range append(append([]ScopeArming{}, p.CommitGate...), p.Denials...) {
		if a.Resolvable != ResolveNotRegistered {
			t.Fatalf("%s %s scope: resolvable = %q, want %q", a.Harness, a.Scope, a.Resolvable, ResolveNotRegistered)
		}
	}
}

// TestObservedReadsTheNewestRowPerHarness pins that the commit gate's observed
// reading is the ledger's newest chain for THAT harness. A chain with no harness
// stamp counts for no harness rather than for every one.
func TestObservedReadsTheNewestRowPerHarness(t *testing.T) {
	isolate(t)
	ev := mine.EvidenceSet{Chains: []mine.Chain{
		{Harness: "claude-code", HarnessKnown: true, EndTS: 1000.0},
		{Harness: "claude-code", HarnessKnown: true, EndTS: 2000.0},
		{Harness: "codex", HarnessKnown: true, EndTS: 1500.0},
		{EndTS: 9000.0},
	}}

	observed := observedChannels(ev)

	if observed.gate["claude-code"] != 2000 {
		t.Fatalf("claude-code observed = %v, want 2000", observed.gate["claude-code"])
	}
	if observed.gate["codex"] != 1500 {
		t.Fatalf("codex observed = %v, want 1500", observed.gate["codex"])
	}
	if len(observed.gate) != 2 {
		t.Fatalf("unstamped chain credited to %v", observed.gate)
	}
}
