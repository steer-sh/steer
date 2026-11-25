package mine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExactRootScopesDoNotPoolDistinctCheckouts checks both corpus windows and binding ownership.
func TestExactRootScopesDoNotPoolDistinctCheckouts(t *testing.T) {
	base := t.TempDir()
	roots := []string{filepath.Join(base, "api-v2"), filepath.Join(base, "api", "v2")}
	for i, root := range roots {
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "init", "-q", root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("initialize checkout: %v: %s", err, out)
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		roots[i] = filepath.ToSlash(resolved)
	}
	own, other := roots[0], roots[1]
	sm, err := BuildScope(false, nil, "", own, "")
	if err != nil {
		t.Fatal(err)
	}
	scope, _, err := ParseScope(Record{"scope": sm})
	if err != nil {
		t.Fatal(err)
	}
	chains := []Chain{
		{StartTS: 100, EndTS: 101, RepoRoot: own, RepoRootKnown: true, Findings: []Finding{{Verdict: "ACCEPTED", Text: "class failure"}}},
		{StartTS: 110, EndTS: 111, RepoRoot: other, RepoRootKnown: true, Findings: []Finding{{Verdict: "ACCEPTED", Text: "class failure"}}},
		{StartTS: 300, EndTS: 301, RepoRoot: own, RepoRootKnown: true},
		{StartTS: 310, EndTS: 311, RepoRoot: other, RepoRootKnown: true, Findings: []Finding{{Verdict: "ACCEPTED", Text: "class failure"}}},
	}
	sessions := []SessionUnit{
		{SessionID: "own-before", StartTS: 100, EndTS: 101, RepoRoot: own, Signals: []SessionSignal{{Kind: SignalCorrection, Text: "class failure"}}},
		{SessionID: "other-before", StartTS: 110, EndTS: 111, RepoRoot: other, Signals: []SessionSignal{{Kind: SignalCorrection, Text: "class failure"}}},
		{SessionID: "own-after", StartTS: 300, EndTS: 301, RepoRoot: own},
		{SessionID: "other-after", StartTS: 310, EndTS: 311, RepoRoot: other, Signals: []SessionSignal{{Kind: SignalCorrection, Text: "class failure"}}},
	}
	if scope.Classify(chains[1]) != Ineligible || scope.ClassifySubject(sessions[1].Subject()) != Ineligible {
		t.Fatal("distinct checkout was admitted by an exact-root scope")
	}
	normalized := Subject{RepoRoot: strings.ReplaceAll(own+"/", "/", "\\"), RepoRootKnown: true}
	if scope.ClassifySubject(normalized) != Eligible {
		t.Fatal("separator and trailing-separator normalization was lost")
	}
	if scope.ClassifySubject((SessionUnit{Project: "legacy-project"}).Subject()) != Unknown {
		t.Fatal("legacy discovery key supplied a guessed root")
	}
	if got := baselineChains(chains, scope, 200, 400); len(got) != 1 || got[0].RepoRoot != own {
		t.Fatalf("chain baseline pools another checkout: %+v", got)
	}
	if got := baselineSessions(sessions, scope, 200); len(got) != 1 || got[0].RepoRoot != own {
		t.Fatalf("session baseline pools another checkout: %+v", got)
	}
	record := Record{"id": "root", "class": "failure", "match": "class failure", "tier": "guard", "installed_ts": 200, "scope": sm}
	chainScore, err := ScoreRecord(record, EvidenceSet{Chains: chains})
	if err != nil {
		t.Fatal(err)
	}
	record["corpus"], record["unit"] = CorpusTranscript, UnitSession
	sessionScore, err := ScoreSessionRecord(record, EvidenceSet{Sessions: sessions})
	if err != nil {
		t.Fatal(err)
	}
	for _, scored := range []map[string]any{chainScore, sessionScore} {
		before := scored["before"].(map[string]any)
		after := scored["after"].(map[string]any)
		if numInt(before["chains"]) != 1 || numInt(before["hits"]) != 1 || numInt(after["chains"]) != 1 || numInt(after["hits"]) != 0 {
			t.Fatalf("scoring pools the other checkout: before=%v after=%v", before, after)
		}
	}
	if err := requireCheckScope("check", map[string]any{CheckRepoKey: other}, sm); err == nil {
		t.Fatal("check binding accepted a different checkout's scope")
	}
	if err := requireCheckScope("check", map[string]any{CheckRepoKey: own}, sm); err != nil {
		t.Fatalf("check binding rejected its own checkout: %v", err)
	}
}
