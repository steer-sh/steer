package status

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestCollectListsTheInventoryScopesItIsGiven: the command lists both scopes' guards
// and presets, and a caller naming none (the TUI, which mutates the scope it lists)
// reads its own scope alone.
func TestCollectListsTheInventoryScopesItIsGiven(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"),
		`{"hooks":{"no-console":{"file":"no-console.py","rule":"global rule"}}}`)
	writeStore(t, filepath.Join(".steer", "hooks"),
		`{"hooks":{"no-console":{"file":"no-console.py","rule":"project rule"}}}`)

	both, err := Collect(Options{GateStateDir: t.TempDir(), Scope: claude.ProjectScope,
		InventoryScopes: []string{claude.ProjectScope, claude.GlobalScope}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range both.Hooks {
		got = append(got, h.Scope+"/"+h.Name+"="+h.Rule)
	}
	if want := "project/no-console=project rule,global/no-console=global rule"; strings.Join(got, ",") != want {
		t.Errorf("both scopes listed %v; want %s", got, want)
	}
	for _, p := range both.Presets {
		if p.Scope == "" {
			t.Errorf("preset %s names no scope", p.Name)
		}
	}

	one, err := Collect(Options{GateStateDir: t.TempDir(), Scope: claude.GlobalScope})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Hooks) != 1 || one.Hooks[0].Scope != claude.GlobalScope {
		t.Errorf("a caller naming no inventory scope listed %+v; want its own scope alone", one.Hooks)
	}
	for _, p := range one.Presets {
		if p.Scope != claude.GlobalScope {
			t.Errorf("preset %s listed from scope %q; want global alone", p.Name, p.Scope)
		}
	}
}

// TestContractNamesEveryFixWithinItsSizeCeiling: an eight-record snapshot with eight
// guards in two scopes, each with a full fixture receipt, names every record and every
// guard, carries no per-fix observation, and stays under 30KB indented and 20KB compact.
func TestContractNamesEveryFixWithinItsSizeCeiling(t *testing.T) {
	s := eightRecordSnapshot()
	pretty, err := s.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	var decoded statusJSON
	if err := json.Unmarshal(pretty, &decoded); err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(pretty) > 30_000 || len(compact) > 20_000 {
		t.Errorf("contract is %d bytes indented, %d compact; the ceiling is 30000 and 20000", len(pretty), len(compact))
	}
	ids := map[string]bool{}
	for _, iv := range decoded.Interventions {
		ids[iv.ID] = true
	}
	for _, iv := range s.Interventions {
		if !ids[iv.ID] {
			t.Errorf("contract omits fix %s", iv.ID)
		}
	}
	hooks := map[string]bool{}
	for _, h := range decoded.Hooks {
		hooks[h.Scope+"/"+h.Name] = true
	}
	for _, h := range s.Hooks {
		if !hooks[h.Scope+"/"+h.Name] {
			t.Errorf("contract omits guard %s/%s", h.Scope, h.Name)
		}
	}
	for _, key := range []string{`"observation"`, `"match"`, `"legacy_note"`, `"before_coverage"`, `"chains": [`} {
		if strings.Contains(string(pretty), key) {
			t.Errorf("contract carries %s, which `steer fix score --json` owns", key)
		}
	}
}

// eightRecordSnapshot is a snapshot sized like a working machine: eight records
// with full observations, three harnesses in two scopes, and a guard store in each.
func eightRecordSnapshot() *SystemStatus {
	prose := strings.Repeat("a sentence that explains the record in the words of its author. ", 3)
	s := &SystemStatus{StatusVersion: StatusVersion, GeneratedAt: "2026-01-01T00:00:00Z",
		Summary: VerdictSummary{Total: 8, Counts: map[string]int{ActionObserve: 8}}}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		for _, h := range []string{"claude-code", "codex", "pi"} {
			arm := ScopeArming{Harness: h, Scope: scope, Registered: true, Effective: true, Timeout: 360,
				Trust: TrustRecordPresent, Command: "/home/u/.steer/bin/steer commit-gate review",
				Resolvable: ResolveResolvable, Observed: 1790000000, ObservedCorpus: ledgerCorpus}
			s.Posture.CommitGate = append(s.Posture.CommitGate, arm)
			s.Posture.Resolver = append(s.Posture.Resolver, arm)
			s.Posture.Masters = append(s.Posture.Masters, MasterRuntime{Harness: h, Scope: scope,
				Path: "/home/u/.steer/hooks/steer-hooks.py", Runtime: "fa0e2d6a", Version: "0.1.0",
				Expected: "fa0e2d6a", State: MasterCurrent, Trust: TrustRecordPresent, Observed: 1790000000,
				Sources: &claude.SourceComparison{StoreDir: "/home/u/.steer/hooks",
					MasterPath: "/home/u/.steer/hooks/steer-hooks.py", State: claude.ComparisonComplete,
					Guards: []claude.GuardSource{{Name: "g1", State: claude.SourceMatches}, {Name: "g2", State: claude.SourceMatches}}}})
			s.Posture.Rules = append(s.Posture.Rules, RulesPresence{Harness: h, Scope: scope,
				Path: "/home/u/.claude/CLAUDE.md", Expected: 2, Missing: []string{}, Extra: []string{}, State: RulesCurrent})
		}
		for i := 0; i < 4; i++ {
			cases, shadowed := 14, 1
			s.Hooks = append(s.Hooks, HookInfo{Name: fmt.Sprintf("guard-%d", i), Scope: scope,
				Source: "generated", Rule: "never run the command this guard names in a shell",
				Fixtures: &mine.FixtureReceipt{Applicable: true, State: mine.FixtureStale, Cases: &cases,
					Shadowed: &shadowed, VerifiedAt: "2026-01-01T00:00:00Z",
					Changed: []string{"fixtures", "hook", "rule", "master"},
					Reason:  "the master hook could not be read: permission denied"}})
		}
		for i := 0; i < 12; i++ {
			s.Presets = append(s.Presets, PresetInfo{Name: fmt.Sprintf("preset-%d", i), Scope: scope,
				Bundle: "security", Description: "block a risky command the catalog describes in one line"})
		}
	}
	for i := 0; i < 8; i++ {
		iv := ScoredIntervention{
			ID: fmt.Sprintf("record-%d", i), Class: "a recurring problem", Problem: prose, Intervention: prose,
			LegacyNote: prose, Match: `\b(a|long|regex)\b[^.]{0,90}\b(that|defines|the|class)`,
			Tier: "check", Target: "check.sh", Status: "active",
			Corpus: mine.CorpusReviewFindings, Unit: mine.UnitChain,
			Before: cadence.Incidence{Hits: 4, Units: 223, Rate: 0.0179}, After: cadence.Incidence{Hits: 1, Units: 21, Rate: 0.0476},
			Scope: &cadence.Scope{Version: 3, Harness: []string{"claude-code", "codex"},
				RepoRoot: "/Volumes/work/projects/steer-skill", Path: `\.go$`},
			Artifact: &cadence.Artifact{ID: "revert-test", Repo: "/Volumes/work/projects/steer-skill", State: cadence.ArtifactPresent},
			Observation: mine.Observation{
				Recurrence: mine.Recurrence{Occurred: true, Matches: 1, ExampleKind: "adjudicated_finding", Example: prose},
				Coverage: mine.ObservationCoverage{
					Before: map[string]any{"total": 444, "eligible": 223, "ineligible": 163, "unknown": 58},
					After:  map[string]any{"total": 30, "eligible": 21, "ineligible": 5, "unknown": 4}},
				Provenance: mine.ObservationProvenance{Kind: mine.ProvenanceReviewer,
					Before: []string{"reviewer/3/aaaaaaaa", "reviewer/3/bbbbbbbb"}, After: []string{"reviewer/3/cccccccc"}},
			},
			BeforeCoverage: cadence.Coverage{Total: 444, Eligible: 223, Ineligible: 163, Unknown: 58},
			AfterCoverage:  cadence.Coverage{Total: 30, Eligible: 21, Ineligible: 5, Unknown: 4},
		}
		s.Interventions = append(s.Interventions, iv)
		s.Attention.Interventions = append(s.Attention.Interventions, cadence.Intervention{
			ID: iv.ID, Class: iv.Class, Problem: prose, Intervention: prose, Match: iv.Match, Observation: iv.Observation})
	}
	s.Chains = make([]ChainSummary, recentChainLimit)
	return s
}

// TestJSONReadinessWithholdsReviewingFromAnUntrustedCodexGate: a Codex registration
// reads effective before Codex trusts the hook, so an agent that reads the contract
// must get the readiness answer, which withholds "reviewing" until trust is known.
func TestJSONReadinessWithholdsReviewingFromAnUntrustedCodexGate(t *testing.T) {
	s := &SystemStatus{StatusVersion: StatusVersion}
	s.Posture.CommitGate = []ScopeArming{{Harness: "codex", Scope: "global", Registered: true,
		Effective: true, Trust: TrustMissing, Resolvable: ResolveResolvable}}
	raw, err := s.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	var decoded statusJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	r := decoded.Posture.Readiness
	if r.Reviewing {
		t.Fatalf("readiness = %+v; an untrusted Codex hook is not reviewing", r)
	}
	if len(r.Unverified) != 1 || !strings.Contains(r.Unverified[0], "no trust record") {
		t.Errorf("unverified = %q; want the Codex trust line", r.Unverified)
	}
}
