package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tier 1 of the port's differential replay: budgetSections, the noise/prose/cruft
// filters, NeutralizeTags and ChangedSymbols are pure functions of the diff text and the
// path list, so every saved diff can be fed to both builders and compared byte for byte.
//
// The corpus is the gate's own saved diffs — private repo content, not committed here.
// Generate expectations with replay_tier1.py; COMMIT_GATE_REPLAY_TIER1 points at them.
type replayCase struct {
	Name           string   `json:"name"`
	DiffSHA        string   `json:"diff_sha"`
	Paths          []string `json:"paths"`
	ProseOnly      bool     `json:"prose_only"`
	Noise          []string `json:"noise"`
	Cruft          []string `json:"cruft"`
	NeutralizedSHA string   `json:"neutralized_sha"`
	Symbols        []string `json:"symbols"`
	Budgets        []struct {
		Budget  int      `json:"budget"`
		OK      bool     `json:"ok"`
		KeptSHA string   `json:"kept_sha"`
		KeptLen int      `json:"kept_len"`
		Dropped []string `json:"dropped"`
	} `json:"budgets"`
}

func TestReplayTier1(t *testing.T) {
	path := os.Getenv("COMMIT_GATE_REPLAY_TIER1")
	if path == "" {
		t.Skip("COMMIT_GATE_REPLAY_TIER1 not set; see replay_tier1.py")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []replayCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "state", "commit-review", "diffs")

	for _, tc := range cases {
		diffBytes, err := os.ReadFile(filepath.Join(dir, tc.Name))
		if err != nil {
			t.Errorf("%s: %v", tc.Name, err)
			continue
		}
		diff := string(diffBytes)
		names := strings.Join(tc.Paths, "\x00")
		if len(tc.Paths) > 0 {
			names += "\x00"
		}

		if got := IsProseOnly(tc.Paths); got != tc.ProseOnly {
			t.Errorf("%s: IsProseOnly = %v, python %v", tc.Name, got, tc.ProseOnly)
		}
		if got := filterPaths(tc.Paths, IsNoise); !equalStrings(got, tc.Noise) {
			t.Errorf("%s: noise = %v, python %v", tc.Name, got, tc.Noise)
		}
		if got := filterPaths(tc.Paths, IsOSCruft); !equalStrings(got, tc.Cruft) {
			t.Errorf("%s: cruft = %v, python %v", tc.Name, got, tc.Cruft)
		}
		if got := sha256hex(NeutralizeTags(diff)); got != tc.NeutralizedSHA {
			t.Errorf("%s: NeutralizeTags digest = %s, python %s", tc.Name, got, tc.NeutralizedSHA)
		}
		if got := sortedKeys(ChangedSymbols(diff, tc.Paths)); !equalStrings(got, tc.Symbols) {
			t.Errorf("%s: ChangedSymbols = %v, python %v", tc.Name, got, tc.Symbols)
		}
		for _, b := range tc.Budgets {
			kept, dropped, ok := budgetSections(diff, names, b.Budget)
			if ok != b.OK {
				t.Errorf("%s @%d: budgetSections ok = %v, python %v", tc.Name, b.Budget, ok, b.OK)
				continue
			}
			if !ok {
				continue
			}
			if got := sha256hex(kept); got != b.KeptSHA {
				t.Errorf("%s @%d: kept digest = %s (%d bytes), python %s (%d bytes)",
					tc.Name, b.Budget, got, len(kept), b.KeptSHA, b.KeptLen)
			}
			if !equalStrings(dropped, b.Dropped) {
				t.Errorf("%s @%d: dropped = %v, python %v", tc.Name, b.Budget, dropped, b.Dropped)
			}
		}
	}
	t.Logf("%d saved diffs replayed", len(cases))
}

func filterPaths(paths []string, pred func(string) bool) []string {
	var out []string
	for _, p := range paths {
		if pred(p) {
			out = append(out, p)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sha256hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
