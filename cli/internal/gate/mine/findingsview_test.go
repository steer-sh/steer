package mine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// findingsDir writes a corpus of one chain per concern set, plus the records that
// decide over it, and returns the state directory holding both.
func findingsDir(t *testing.T, records []Record, chains ...[]map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	var rows []gate.Row
	for i, cs := range chains {
		ts := 1000 + i*100
		// A firing-1 deny opens a new chain, so each concern set gets its own.
		rows = append(rows,
			row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", cs...))
	}
	var ledger bytes.Buffer
	for _, r := range rows {
		ledger.Write(mustJSON(t, r))
		ledger.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "adjudication.test.jsonl"), ledger.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var recs bytes.Buffer
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		recs.Write(data)
		recs.WriteByte('\n')
	}
	if err := os.WriteFile(InterventionsPath(dir), recs.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func findingsOut(t *testing.T, dir string, o FindingsOpts) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Findings(&buf, dir, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestUndecidedDropsAFullyCoveredChain(t *testing.T) {
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")},
		[]map[string]any{c("a defect no class names")})

	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	if strings.Contains(out, "the parser drops a flag") {
		t.Error("a chain whose every finding is covered must not be shown")
	}
	if !strings.Contains(out, "a defect no class names") {
		t.Error("the undecided chain must still be shown")
	}
}

func TestUndecidedKeepsAMixedChainAndMarksTheCoveredFinding(t *testing.T) {
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag"), c("a defect no class names")})

	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	if !strings.Contains(out, "[covered-by=known] the parser drops a flag") {
		t.Errorf("the covered finding stays in place, marked; got:\n%s", out)
	}
	if !strings.Contains(out, "] a defect no class names") ||
		strings.Contains(out, "covered-by=known] a defect no class names") {
		t.Errorf("the residual finding carries no marker; got:\n%s", out)
	}
}

func TestUndecidedKeepsTheChainDenominator(t *testing.T) {
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")},
		[]map[string]any{c("a defect no class names")})

	var payload struct {
		ChainsReviewed int `json:"chains_reviewed"`
		Chains         []struct {
			Findings []RenderedFinding `json:"findings"`
		} `json:"chains"`
		Covered  int `json:"covered_findings"`
		Residual int `json:"residual_findings"`
	}
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true, JSON: true})
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	// Narrowing what the reader is shown must not narrow what every rate is over.
	if payload.ChainsReviewed != 2 {
		t.Errorf("the denominator stays the whole corpus; want 2, got %d", payload.ChainsReviewed)
	}
	if len(payload.Chains) != 1 {
		t.Errorf("one chain survives the filter; got %d", len(payload.Chains))
	}
	if payload.Covered != 1 || payload.Residual != 1 {
		t.Errorf("counts span the corpus, not the kept chains; covered %d residual %d",
			payload.Covered, payload.Residual)
	}
}

func TestUndecidedCountsAFindingOnceUnderTwoClasses(t *testing.T) {
	dir := findingsDir(t,
		[]Record{
			reviewRecord("first", "parser", "defect", "active"),
			reviewRecord("second", "drops", "defect", "active"),
		},
		[]map[string]any{c("the parser drops a flag")})

	var payload struct {
		Covered  int `json:"covered_findings"`
		Selected int `json:"selected_findings"`
	}
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true, JSON: true})
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Selected != 1 || payload.Covered != 1 {
		t.Errorf("two classes covering one finding count it once; selected %d covered %d",
			payload.Selected, payload.Covered)
	}
}

func TestDefaultViewCarriesNoCoverage(t *testing.T) {
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")})

	text := findingsOut(t, dir, FindingsOpts{Kind: "accepted"})
	if strings.Contains(text, "covered-by=") {
		t.Error("the default dump marks nothing")
	}
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", JSON: true})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"covered_findings", "residual_findings", "selected_findings", "residual_chains"} {
		if _, ok := payload[key]; ok {
			t.Errorf("the default dump emits the JSON it emitted before the view existed; found %q", key)
		}
	}
	if strings.Contains(out, "covered_by") {
		t.Error("a finding carries no coverage field outside the undecided view")
	}
}

func TestUndecidedHeaderCountsDescribeTheCorpus(t *testing.T) {
	// Two chains carry an accepted finding; one of them is fully covered and is not
	// printed. Every header count must still describe the corpus, so no line reports
	// the printed subset under a name that means the whole of it.
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")},
		[]map[string]any{c("a defect no class names")})

	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	for _, want := range []string{
		"# 2 chains reviewed, 2 carrying accepted findings",
		"# 2 distinct findings",
		"# --undecided: 1 covered, 1 remaining over 1 chain(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want header line %q; got:\n%s", want, out)
		}
	}
}
