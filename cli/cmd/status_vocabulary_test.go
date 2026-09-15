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
			name: "transcript pattern",
			in: cadence.Candidate{Corpus: "transcript-friction", Pattern: "bare python call",
				Count: 1, Sessions: 1},
			want: []string{"Bare python call", "a repeated pattern in your session history",
				"1 recurrence", "1 agent session"},
			old: []string{"transcript-friction", "hits", "session(s)"},
		},
		{
			name: "review finding queue",
			in: cadence.Candidate{Corpus: "review-findings", UndecidedChains: 2,
				Dump: "steer commit-gate mine findings"},
			want: []string{"2 review sequences carry a finding no problem covers yet",
				"steer commit-gate mine findings"},
			old: []string{"review-findings", "review sequence(s)"},
		},
		{
			name: "review finding pattern",
			in:   cadence.Candidate{Corpus: "review-findings", Pattern: "missing scope", Count: 2, Sessions: 3},
			want: []string{"a repeated review finding in your session history", "2 recurrences", "3 agent sessions"},
			old:  []string{"review-findings", "hits", "session(s)"},
		},
		{
			name: "other corpus pattern",
			in:   cadence.Candidate{Corpus: "other-corpus", Pattern: "blocked command", Count: 2, Sessions: 3},
			want: []string{"a repeated pattern in your session history", "2 recurrences", "3 agent sessions"},
			old:  []string{"other-corpus", "hits", "session(s)"},
		},
		{
			name: "empty unpatterned queue item",
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
