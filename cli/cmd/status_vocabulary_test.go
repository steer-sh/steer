package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/status"
)

// The dashboard is the reader-facing vocabulary contract. These cases exercise
// values that arrive as data, which the source-literal vocabulary test cannot see.
func TestStatusCandidateLinesUseDashboardVocabulary(t *testing.T) {
	cases := []struct {
		name string
		in   cadence.Candidate
		want []string
		old  []string
	}{
		{
			name: "transcript pile",
			in: cadence.Candidate{Corpus: "transcript-friction", UngroupedClusters: 12,
				Examples: []string{"no let s first check the scorm file"}},
			want: []string{"Correction snippets not yet grouped into a problem",
				"12 correction snippets in your session history that no problem covers yet"},
			// The title may never be an item of the pile, and a count of phrasings
			// may never be called a recurrence: both claimed a grouping that nothing
			// had made.
			old: []string{"transcript-friction", "no let s first check", "recurrence", "agent session"},
		},
		{
			name: "transcript pile of one",
			in:   cadence.Candidate{Corpus: "transcript-friction", UngroupedClusters: 1},
			want: []string{"1 correction snippet in your session history"},
			old:  []string{"1 correction snippets", "recurrence"},
		},
		{
			name: "review finding queue",
			in: cadence.Candidate{Corpus: "review-findings", UndecidedChains: 2,
				Dump: "steer commit-gate mine findings"},
			want: []string{"Review findings not yet grouped into a problem",
				"2 review sequences carry a finding no problem covers yet",
				"steer commit-gate mine findings"},
			old: []string{"review-findings", "review sequence(s)", "recurrence"},
		},
		{
			name: "unknown corpus",
			in:   cadence.Candidate{Corpus: "other-corpus"},
			want: []string{"Review findings not yet grouped into a problem",
				"0 review sequences carry a finding no problem covers yet"},
			old: []string{"other-corpus"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := candidateBlock(tc.in, 100)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("candidateBlock = %q; missing %q", got, want)
				}
			}
			for _, old := range tc.old {
				if strings.Contains(got, old) {
					t.Errorf("candidateBlock = %q; still uses %q", got, old)
				}
			}
		})
	}
}

func TestStatusHumanSectionsUseDashboardVocabulary(t *testing.T) {
	s := &status.SystemStatus{
		Posture: status.Posture{
			Reviewer:   "reviewer",
			Ledger:     "review.log",
			Instrument: "instrument",
			CommitGate: []status.ScopeArming{
				{Harness: "claude-code", Scope: "global"},
				{Harness: "codex", Scope: "global", Registered: true},
				{Harness: "pi", Scope: "global", Registered: true, Resolvable: status.ResolveMissing},
			},
			Resolver: []status.ScopeArming{{Harness: "claude-code", Scope: "global",
				Registered: true}},
			Masters: []status.MasterRuntime{
				{Harness: "claude-code", Scope: "global", State: status.MasterCurrent,
					Runtime: "runtime", Version: "version"},
				{Harness: "codex", Scope: "global", State: status.MasterStale,
					Runtime: "old-runtime", Expected: "runtime"},
			},
		},
		Corpora: []status.CorpusRow{
			{Corpus: "review-findings", Present: true, Size: 2},
			{Corpus: "transcript-friction", Present: true, Size: 3},
		},
		Telemetry: status.Telemetry{AuditFirings: 3},
	}

	posture := captureStdout(t, func() { renderPosture(s, 100) })
	for _, want := range []string{
		"Arming", "Claude Code", "Guard runner",
		"off", "inert", "broken", "review findings", "session history",
		"Guard blocks",
	} {
		if !strings.Contains(posture, want) {
			t.Errorf("posture output is missing dashboard term %q:\n%s", want, posture)
		}
	}
	for _, old := range []string{
		"Posture", "Denials      ", "Resolver     ", "Read outcomes", "Guard master", "not armed",
		"registered but inert", "action(s) blocked", "review-findings",
		"transcript-friction", "auto-denials",
	} {
		if strings.Contains(posture, old) {
			t.Errorf("posture output still uses retired term %q:\n%s", old, posture)
		}
	}
	if strings.Contains(strings.ToLower(posture), "scope: guard runner") {
		t.Errorf("posture output repeats the runner noun in its runtime detail:\n%s", posture)
	}

	attention := captureStdout(t, func() {
		renderAttention(&status.SystemStatus{Attention: status.AttentionQueue{
			Candidates: []cadence.Candidate{{Corpus: "review-findings", UndecidedChains: 1}},
		}}, 100)
	})
	if !strings.Contains(attention, "Problems") || strings.Contains(attention, "Attention Required") {
		t.Errorf("attention section does not use the Problems vocabulary:\n%s", attention)
	}

	empty := captureStdout(t, func() {
		renderAttention(&status.SystemStatus{}, 100)
	})
	if !strings.Contains(empty, "no problems recorded on this machine") ||
		strings.Contains(empty, "no fixes recorded on this machine") {
		t.Errorf("empty summary has stale vocabulary:\n%s", empty)
	}
}
