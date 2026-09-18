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
	"github.com/steer-sh/steer-cli/internal/pi"
)

// isolate points both homes at a temp dir so the audit-log count
// and every harness posture reader resolve inside the sandbox, never the real machine.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows, which is a shipped binary target.
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
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
	// The class was absent after install; the observation records that fact without
	// turning it into an efficacy claim.
	if iv.Observation.Recurrence.Occurred {
		t.Error("class should not recur in the after window")
	}
	if iv.Before.Hits == 0 {
		t.Error("the before window should carry the class")
	}
	if iv.After.Hits != 0 {
		t.Errorf("the after window eliminated the class, got %d hits", iv.After.Hits)
	}
	if s.Summary.Counts[ActionRepair] != 1 {
		t.Errorf("summary should count the repair action once, got %+v", s.Summary.Counts)
	}

	// Telemetry: 24 chains total (12 before + 12 after), no audit log.
	if s.Telemetry.ReviewChains != 24 {
		t.Errorf("want 24 review chains, got %d", s.Telemetry.ReviewChains)
	}
	if s.Telemetry.AuditFirings != 0 {
		t.Errorf("an empty machine should report zero firings, got %+v", s.Telemetry)
	}
	if len(s.Chains) == 0 {
		t.Error("recent chains should not be empty")
	}
	// One corpus row per corpus, from the cadence sweep.
	if len(s.Corpora) != 2 {
		t.Errorf("want 2 corpus rows from the sweep, got %d", len(s.Corpora))
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
	// Posture is harness-qualified: the commit gate and its resolver report every
	// enforcing harness in both scopes, and nothing is armed.
	if len(s.Posture.CommitGate) != 6 || len(s.Posture.Resolver) != 6 {
		t.Errorf("gate posture should report all harnesses in both scopes, got %+v", s.Posture)
	}
	for _, row := range s.Posture.CommitGate {
		if row.Harness == "" {
			t.Errorf("every posture row names its harness, got %+v", row)
		}
		if row.Registered || row.Effective {
			t.Errorf("an empty machine arms nothing, got %+v", row)
		}
	}
	// Codex and Pi review hook trust, so a registration there proves no enforcement
	// and Trust reads unknown. Effective is the registration's own shape, not trust.
	for _, row := range s.Posture.CommitGate {
		want := TrustNotApplicable
		if row.Harness == "codex" || row.Harness == "pi" {
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
		StatusVersion: 24,
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
			Resolver: []ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true, Timeout: 30,
					Trust: TrustNotApplicable, Command: "/h/.steer/bin/steer commit-gate backfill",
					Resolvable: ResolveResolvable},
				{Harness: "codex", Scope: "global", Registered: false, Effective: false,
					Trust: TrustMissing, Resolvable: ResolveNotRegistered},
			},
			Masters: []MasterRuntime{
				{Harness: "claude-code", Scope: "global", Path: "/h/steer-hooks.py",
					Runtime: "aaaaaaaa", Version: "0.1.0", Expected: "bbbbbbbb", State: MasterStale, Trust: TrustNotApplicable, Observed: 1690},
				{Harness: "codex", Scope: "global", Expected: "bbbbbbbb", State: MasterNotRegistered},
				{Harness: "codex", Scope: "project", Expected: "bbbbbbbb", State: MasterDetached,
					Store: ".steer/hooks/steer-hooks.py"},
			},
			Rules: []RulesPresence{
				{Harness: "codex", Scope: "global", Path: "/h/.codex/AGENTS.md", Expected: 3,
					Missing: []string{"config-path"}, Extra: []string{}, State: RulesDrifted},
			},
			Unresolved: 4,
		},
		Checks: CheckTier{
			Repo: "/w/steer", Manifest: true, Adopted: true, AdoptedAt: 1600,
			Runner: []ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true, Timeout: 330,
					Trust: TrustNotApplicable, Command: "/h/.steer/bin/steer check hook --harness claude-code",
					Resolvable: ResolveResolvable, Observed: 1710, ObservedCorpus: checkCorpus},
				{Harness: "codex", Scope: "global", Registered: false, Effective: false,
					Trust: TrustUnknown, Resolvable: ResolveNotRegistered, ObservedCorpus: checkCorpus},
			},
			Checks: []CheckRow{{
				ID: "json-contract", Kind: "go-test", Binding: "cli/internal/status TestJSONContractIsStable",
				Validation: CheckStale, Changed: []string{"detector"}, Receipt: "r1", ValidatedAt: 1500,
				Execution: "passed", ExecutedAt: 1710, ExecutionRun: "run-2",
			}},
			LatestRun: &CheckRun{RunID: "run-2", Source: "stop", Harness: "claude-code",
				Outcome: "passed", Disposition: "allow", Finished: 1710, RefusedAfter: 1712},
		},
		Interventions: []ScoredIntervention{{
			ID: "widget-flag", Class: "widget flag loss",
			Problem:      "the parser drops a flag and the reviewer catches it after the fact",
			Intervention: "a guard refuses a parser branch that drops a flag",
			LegacyNote:   "guard tier 2",
			Match:        "widget.*flag", Tier: "guard", Target: "guard.py",
			Status: "active", HookID: "no-widget-flag",
			Corpus: mine.CorpusReviewFindings, Unit: mine.UnitChain,
			Before: cadence.Incidence{Hits: 12, Units: 12, Rate: 1}, After: cadence.Incidence{Hits: 0, Units: 12, Rate: 0},
			Observation: mine.Observation{
				Firing:     mine.FiringActivity{Applicable: true, LogPresent: true, HookID: "no-widget-flag", Scope: mine.FiringScopeWindowPeriod, Rows: 3, Units: 2},
				Coverage:   mine.ObservationCoverage{Before: map[string]any{"total": 14, "eligible": 12, "ineligible": 1, "unknown": 1, "unknown_scope": 1}, After: map[string]any{"total": 13, "eligible": 12, "unknown": 1, "unknown_evidence": 1, "pending": 1}, ZeroEligibleAfter: false},
				Provenance: mine.ObservationProvenance{Kind: mine.ProvenanceReviewer, Before: []string{"a"}, After: []string{"a", "b"}},
			},
			Artifact: &cadence.Artifact{ID: "no-widget-flag", Scope: "global", State: cadence.ArtifactPresent,
				Removed: "2026-01-02T03:04:05.000000000Z"},
			Scope:          &cadence.Scope{Version: 1, Repo: "steer.*", Path: `\.go$`},
			BeforeCoverage: cadence.Coverage{Total: 14, Eligible: 12, Ineligible: 1, Unknown: 1, UnknownScope: 1},
			AfterCoverage:  cadence.Coverage{Total: 15, Eligible: 12, Unknown: 3, UnknownEvidence: 3, Pending: 1, LedgerMissed: 1, LedgerOverdue: 1, PartialExposure: 1},
		}, {
			ID: "taste-nit", Class: "taste nit",
			Problem:      "the reviewer raises a wording preference with no defect behind it",
			Intervention: "no suitable intervention was identified",
			Match:        "nit", Tier: "check", Target: "check.sh",
			Status: "rejected",
			Corpus: mine.CorpusTranscript, Unit: mine.UnitSession, SignalKind: mine.SignalCorrection,
			Reason: "not mechanizable", Reopen: "incidence doubles", RejectedTS: 1700,
			Scope: &cadence.Scope{Version: 1, All: true},
			Observation: mine.Observation{Declined: true, Checks: mine.CheckActivity{
				Applicable: true, Attribution: mine.AttributionCheckID, StreamPresent: true, RegisteredHost: "host-a",
				CheckID: "json-contract", Repo: "/w/steer", Scope: mine.FiringScopeEligibleUnits, ClassScoped: true,
				Executions: 4, Outcomes: map[string]int{"failed": 1, "passed": 3}, Blocked: 1, ChangedDetector: 1,
				Units: 2, NewestTS: 1710,
			}, Coverage: mine.ObservationCoverage{Before: map[string]any{}, After: map[string]any{}, ZeroEligibleAfter: true}, Provenance: mine.ObservationProvenance{Kind: mine.ProvenanceParser}},
		}},
		Summary: VerdictSummary{Total: 2, Counts: map[string]int{ActionRepair: 1, ActionObserve: 1}},
		Chains: []ChainSummary{{
			Host: "h", StartTS: 1000, Rounds: 2, Files: 1, Findings: 1, Accepted: 1, Sessions: 1, Terminal: "clean",
		}},
		Attention: AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
			Checks: []cadence.CheckWork{{
				Kind: cadence.CheckNeverValidated, Repo: "/w/steer", CheckID: "json-contract",
				Detail: "no replay recorded here", Fix: "steer check test json-contract",
			}},
		},
		Telemetry: Telemetry{
			ReviewChains: 24, AuditFirings: 3, SessionSnapshots: 40,
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
		Rules:   []RuleInfo{{ID: "config-path", Scope: "global", Text: "resolve config from the repo root"}},
		Addenda: []AddendumInfo{{ID: "fp-sort", Scope: "project", Text: "sorted output is fine",
			HeadText: "sorted output is not a finding", State: AddendumUncommittedChange}},
		Team:          TeamState{Present: true, Path: "steer-team.lock.json", HooksLocked: 3, CatalogVersion: "v7"},
		StoreProblems: []StoreProblem{{Scope: "project", Error: "guard manifest /w/.steer/hooks/steer-manifest.json gives entry \"x\" the file \"../y\", which is not a plain file name; edit the manifest entry"}},
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
  "status_version": 24,
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
        "trust": "not-applicable",
        "observed": 1690
      },
      {
        "harness": "codex",
        "scope": "global",
        "expected": "bbbbbbbb",
        "state": "not-registered"
      },
      {
        "harness": "codex",
        "scope": "project",
        "expected": "bbbbbbbb",
        "state": "detached",
        "store": ".steer/hooks/steer-hooks.py"
      }
    ],
    "rules": [
      {
        "harness": "codex",
        "scope": "global",
        "path": "/h/.codex/AGENTS.md",
        "expected": 3,
        "missing": [
          "config-path"
        ],
        "extra": [],
        "state": "drifted"
      }
    ]
  },
  "checks": {
    "repo": "/w/steer",
    "manifest": true,
    "adopted": true,
    "adopted_at": 1600,
    "runner": [
      {
        "harness": "claude-code",
        "scope": "global",
        "registered": true,
        "effective": true,
        "timeout": 330,
        "trust": "not-applicable",
        "command": "/h/.steer/bin/steer check hook --harness claude-code",
        "resolvable": "resolvable",
        "observed": 1710,
        "observed_corpus": "check-runs"
      },
      {
        "harness": "codex",
        "scope": "global",
        "registered": false,
        "effective": false,
        "trust": "unknown",
        "resolvable": "not-registered",
        "observed_corpus": "check-runs"
      }
    ],
    "checks": [
      {
        "id": "json-contract",
        "kind": "go-test",
        "binding": "cli/internal/status TestJSONContractIsStable",
        "validation": "stale",
        "changed": [
          "detector"
        ],
        "receipt": "r1",
        "validated_at": 1500,
        "execution": "passed",
        "executed_at": 1710,
        "execution_run": "run-2"
      }
    ],
    "latest_run": {
      "run_id": "run-2",
      "source": "stop",
      "harness": "claude-code",
      "outcome": "passed",
      "disposition": "allow",
      "finished": 1710,
      "refused_after": 1712
    }
  },
  "interventions": [
    {
      "id": "widget-flag",
      "class": "widget flag loss",
      "problem": "the parser drops a flag and the reviewer catches it after the fact",
      "intervention": "a guard refuses a parser branch that drops a flag",
      "legacy_note": "guard tier 2",
      "match": "widget.*flag",
      "tier": "guard",
      "target": "guard.py",
      "status": "active",
      "hook_id": "no-widget-flag",
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
      "observation": {
        "counts": {
          "split_ts": 0,
          "observed_ts": 0,
          "unit": "",
          "before": {
            "hits": 0,
            "eligible": 0,
            "first_activity_ts": 0,
            "last_activity_ts": 0
          },
          "after": {
            "hits": 0,
            "eligible": 0,
            "first_activity_ts": 0,
            "last_activity_ts": 0
          }
        },
        "recurrence": {
          "occurred": false,
          "matches": 0
        },
        "firing": {
          "applicable": true,
          "log_present": true,
          "hook_id": "no-widget-flag",
          "scope": "window_period",
          "class_scoped": false,
          "rows": 3,
          "units": 2
        },
        "checks": {
          "applicable": false,
          "stream_present": false,
          "other_host": false,
          "class_scoped": false,
          "executions": 0,
          "blocked": 0,
          "changed_detector": 0,
          "units": 0
        },
        "coverage": {
          "before": {
            "eligible": 12,
            "ineligible": 1,
            "total": 14,
            "unknown": 1,
            "unknown_scope": 1
          },
          "after": {
            "eligible": 12,
            "pending": 1,
            "total": 13,
            "unknown": 1,
            "unknown_evidence": 1
          },
          "zero_eligible_after": false
        },
        "provenance": {
          "kind": "reviewer",
          "before": [
            "a"
          ],
          "after": [
            "a",
            "b"
          ]
        },
        "declined": false
      },
      "artifact": {
        "id": "no-widget-flag",
        "scope": "global",
        "state": "present",
        "removed": "2026-01-02T03:04:05.000000000Z"
      },
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
        "ledger_missed": 0,
        "ledger_overdue": 0,
        "partial_exposure": 0
      },
      "after_coverage": {
        "total": 15,
        "eligible": 12,
        "ineligible": 0,
        "unknown": 3,
        "unknown_scope": 0,
        "unknown_evidence": 3,
        "pending": 1,
        "ledger_missed": 1,
        "ledger_overdue": 1,
        "partial_exposure": 1
      },
      "corpus": "review-findings",
      "unit": "chain"
    },
    {
      "id": "taste-nit",
      "class": "taste nit",
      "problem": "the reviewer raises a wording preference with no defect behind it",
      "intervention": "no suitable intervention was identified",
      "match": "nit",
      "tier": "check",
      "target": "check.sh",
      "status": "rejected",
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
      "observation": {
        "counts": {
          "split_ts": 0,
          "observed_ts": 0,
          "unit": "",
          "before": {
            "hits": 0,
            "eligible": 0,
            "first_activity_ts": 0,
            "last_activity_ts": 0
          },
          "after": {
            "hits": 0,
            "eligible": 0,
            "first_activity_ts": 0,
            "last_activity_ts": 0
          }
        },
        "recurrence": {
          "occurred": false,
          "matches": 0
        },
        "firing": {
          "applicable": false,
          "log_present": false,
          "class_scoped": false,
          "rows": 0,
          "units": 0
        },
        "checks": {
          "applicable": true,
          "attribution": "check-id",
          "stream_present": true,
          "registered_host": "host-a",
          "other_host": false,
          "check_id": "json-contract",
          "repo": "/w/steer",
          "scope": "eligible_units",
          "class_scoped": true,
          "executions": 4,
          "outcomes": {
            "failed": 1,
            "passed": 3
          },
          "blocked": 1,
          "changed_detector": 1,
          "units": 2,
          "newest_ts": 1710
        },
        "coverage": {
          "before": {},
          "after": {},
          "zero_eligible_after": true
        },
        "provenance": {
          "kind": "parser",
          "before": null,
          "after": null
        },
        "declined": true
      },
      "artifact": null,
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
        "ledger_missed": 0,
        "ledger_overdue": 0,
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
        "ledger_missed": 0,
        "ledger_overdue": 0,
        "partial_exposure": 0
      },
      "corpus": "transcript-friction",
      "unit": "session",
      "signal_kind": "user_corrections"
    }
  ],
  "summary": {
    "total": 2,
    "counts": {
      "observe": 1,
      "repair": 1
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
    "candidates": [],
    "checks": [
      {
        "kind": "never-validated",
        "repo": "/w/steer",
        "check_id": "json-contract",
        "detail": "no replay recorded here",
        "fix": "steer check test json-contract"
      }
    ]
  },
  "telemetry": {
    "review_chains": 24,
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
  "rules": [
    {
      "id": "config-path",
      "scope": "global",
      "text": "resolve config from the repo root"
    }
  ],
  "addenda": [
    {
      "id": "fp-sort",
      "scope": "project",
      "text": "sorted output is fine",
      "head_text": "sorted output is not a finding",
      "state": "uncommitted-change"
    }
  ],
  "team": {
    "present": true,
    "path": "steer-team.lock.json",
    "hooks_locked": 3,
    "catalog_version": "v7"
  },
  "store_problems": [
    {
      "scope": "project",
      "error": "guard manifest /w/.steer/hooks/steer-manifest.json gives entry \"x\" the file \"../y\", which is not a plain file name; edit the manifest entry"
    }
  ]
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

	for _, a := range p.CommitGate {
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

// TestPiPostureReadsTheRegisteredExtension pins the Pi rows over an ARMED machine:
// the empty-machine test proves only that the rows exist. Pi writes the review and
// the resolver into one extension file, so both rows read back from one write.
func TestPiPostureReadsTheRegisteredExtension(t *testing.T) {
	isolate(t)
	if _, err := pi.RegisterCommitGateAndResolver(claude.GlobalScope, t.TempDir(),
		"steer commit-gate review-pi", 210,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}

	p := collectPosture(gate.Config{}, mine.EvidenceSet{})

	var gateRow, resolverRow ScopeArming
	for _, a := range p.CommitGate {
		if a.Harness == "pi" && a.Scope == claude.GlobalScope {
			gateRow = a
		}
	}
	for _, a := range p.Resolver {
		if a.Harness == "pi" && a.Scope == claude.GlobalScope {
			resolverRow = a
		}
	}
	if !gateRow.Registered || !gateRow.Effective || gateRow.Timeout != 210 ||
		gateRow.Command != "steer commit-gate review-pi" {
		t.Fatalf("pi gate row = %+v", gateRow)
	}
	if !resolverRow.Registered || !resolverRow.Effective || resolverRow.Timeout != 30 ||
		resolverRow.Command != "steer commit-gate backfill --if-due --apply --quiet" {
		t.Fatalf("pi resolver row = %+v", resolverRow)
	}
	if gateRow.Trust != TrustUnknown || resolverRow.Trust != TrustUnknown {
		t.Fatalf("trust = %q/%q, want unknown: steer reads no Pi trust record",
			gateRow.Trust, resolverRow.Trust)
	}
}

// A team lockfile that is not there is not a fault. One that IS there and cannot
// be parsed is, and reading the two as one absence told a reader nothing was
// configured while a broken file sat at the path.
func TestABrokenTeamLockfileIsNotAnAbsentOne(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := collectTeam(); got.Present || got.Error != "" {
		t.Errorf("no lockfile read as %#v, want absent with no error", got)
	}

	if err := os.WriteFile(teamLockfileName, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collectTeam()
	if !got.Present {
		t.Fatal("a broken lockfile read as absent")
	}
	if got.Error == "" {
		t.Error("a broken lockfile reports no error")
	}
	if got.Path != teamLockfileName {
		t.Errorf("the broken lockfile names no path: %q", got.Path)
	}
}

// A record from a corpus no engine reads stays out of the scored inventory and
// out of the summary total, and reports by id under Unscored.
func TestAnUnreadableCorpusIsReportedUnscoredAndNotScored(t *testing.T) {
	isolate(t)
	stateDir := seedLedger(t, "widget parser flag loss", "widget parser",
		"widget parser drops a flag", "unrelated naming nit", 12)
	records, err := mine.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, mine.Record{
		"id": "orphan", "class": "an orphan class", "match": "orphan",
		"tier": "check", "target": "check.sh", "status": "active",
		"corpus": "made-up-corpus", "unit": "made-up-unit",
	})
	if err := mine.Save(stateDir, records); err != nil {
		t.Fatal(err)
	}

	s, err := Collect(Options{GateStateDir: stateDir, WindowDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, iv := range s.Interventions {
		if iv.ID == "orphan" {
			t.Fatalf("an unreadable corpus must not reach the scored inventory: %+v", iv)
		}
	}
	if s.Summary.Total != 1 {
		t.Errorf("the total counts scored records only, got %d", s.Summary.Total)
	}
	found := false
	for _, id := range s.Attention.Unscored {
		if id == "orphan" {
			found = true
		}
	}
	if !found {
		t.Errorf("the record must still be reported, got unscored %v", s.Attention.Unscored)
	}
}

// A record's binding survives scoring: the inventory and the attention queue both
// carry where the fix stands, and a rule removed from its store reads as absent.
func TestCollectCarriesTheArtifactOfEachRecord(t *testing.T) {
	isolate(t)
	stateDir := seedLedger(t, "widget parser flag loss", "widget parser",
		"widget parser drops a flag", "unrelated naming nit", 3)
	records, err := mine.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	records[0]["tier"], records[0]["rule_id"], records[0]["rule_scope"] = "instruction", "config-path", "global"
	if err := mine.Save(stateDir, records); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".steer", "hooks")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "steer-manifest.json"), []byte(`{"rules":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Collect(Options{GateStateDir: stateDir, WindowDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Interventions) != 1 || s.Interventions[0].Artifact == nil ||
		s.Interventions[0].Artifact.State != cadence.ArtifactAbsent || s.Interventions[0].Artifact.ID != "config-path" {
		t.Fatalf("inventory = %+v; want the rule read as absent", s.Interventions)
	}
	if len(s.Attention.Interventions) != 1 || s.Attention.Interventions[0].Artifact == nil ||
		s.Attention.Interventions[0].Artifact.State != cadence.ArtifactAbsent {
		t.Fatalf("attention = %+v; want the record queued with its artifact", s.Attention.Interventions)
	}
}
