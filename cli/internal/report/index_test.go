package report

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/harness"
)

func cluster(pattern string, sessions ...string) *Cluster {
	c := &Cluster{Pattern: pattern, Count: len(sessions)}
	seen := map[string]struct{}{}
	for _, s := range sessions {
		seen[s] = struct{}{}
		c.Occurrences = append(c.Occurrences, Occurrence{Session: s, Timestamp: "2026-09-01T00:00:00Z", Text: pattern + " in " + s})
	}
	c.Sessions = len(seen)
	return c
}

func sampleDigest() *Digest {
	d := NewDigest(30)
	d.Signals.UserCorrections = []*Cluster{cluster("don't use var", "s1", "s2"), cluster("stop adding docstrings", "s2")}
	d.Signals.UserRejections = []*Cluster{cluster("Bash: rm -rf build", "s3")}
	d.Signals.RepeatedWording = []*Cluster{cluster("commit when complete", "s1", "s6")}
	d.Signals.RepeatedWording[0].SendGroups = 2
	d.Signals.ToolFailures = []*Cluster{cluster("FAIL: TestThing", "s1", "s4")}
	d.Signals.HookBlocks = []*Cluster{cluster("no python bare", "s5")}
	d.InstructionFiles = []InstructionFile{{
		Harness: harness.ClaudeCode, Path: "CLAUDE.md",
		SteerRules: []string{"- no bare python | use python3"}, GuardRules: []string{"- no bare python | use python3"},
		Directives: []Directive{{Section: "Style", Text: "never use var"}, {Section: "Git", Text: "never force push"}},
	}}
	d.assignIDs()
	return d
}

// The first read lists the user's own signals and sizes the machine-output ones;
// an instruction document arrives as its rule lines and a directive count.
func TestIndexListsTheUserSignalsOnly(t *testing.T) {
	data, err := IndexPage(sampleDigest(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range idx.Rows {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "c1,c2,r1,w1" {
		t.Errorf("index rows = %v, want c1,c2,r1,w1", ids)
	}
	if w := idx.Rows[3]; w.SendGroups != 2 || w.Sessions != 2 || w.Count != 2 {
		t.Errorf("repeated wording row = %+v", w)
	}
	if strings.Contains(string(data), "FAIL: TestThing") || strings.Contains(string(data), "never force push") {
		t.Errorf("index carries a machine-output row or a directive:\n%s", data)
	}
	want := map[string]SignalSummary{
		"user_corrections": {Signal: "user_corrections", Clusters: 2, Sessions: 2, InIndex: true},
		"user_rejections":  {Signal: "user_rejections", Clusters: 1, Sessions: 1, InIndex: true},
		"repeated_wording": {Signal: "repeated_wording", Clusters: 1, Sessions: 2, InIndex: true},
		"tool_failures":    {Signal: "tool_failures", Clusters: 1, Sessions: 2},
		"hook_blocks":      {Signal: "hook_blocks", Clusters: 1, Sessions: 1},
	}
	if len(idx.Signals) != len(want) {
		t.Fatalf("signals = %+v", idx.Signals)
	}
	for _, s := range idx.Signals {
		if s != want[s.Signal] {
			t.Errorf("signal %s = %+v, want %+v", s.Signal, s, want[s.Signal])
		}
	}
	if len(idx.InstructionFiles) != 1 || idx.InstructionFiles[0].Directives != 2 || idx.InstructionFiles[0].ID != "i1" {
		t.Errorf("instruction files = %+v", idx.InstructionFiles)
	}
}

// Following next_cursor from the first page reaches every row exactly once, and a
// page holding more than one row stays within the bound.
func TestIndexPagesLoseNoRow(t *testing.T) {
	d := NewDigest(30)
	for i := 0; i < 60; i++ {
		d.Signals.UserCorrections = append(d.Signals.UserCorrections, cluster(fmt.Sprintf("do not use the bare thing %02d", i), fmt.Sprintf("s%d", i)))
	}
	d.assignIDs()

	const bound = 2500
	seen := map[string]int{}
	cursor, pages := 0, 0
	for {
		data, err := IndexPage(d, cursor, bound)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Rows []Row `json:"rows"`
			Page Page  `json:"page"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) > 1 && len(data) > bound {
			t.Errorf("page at %d is %d bytes, over %d", cursor, len(data), bound)
		}
		for _, r := range page.Rows {
			seen[r.ID]++
		}
		pages++
		if page.Page.NextCursor == 0 {
			if page.Page.Remaining != 0 {
				t.Errorf("last page reports %d remaining", page.Page.Remaining)
			}
			break
		}
		cursor = page.Page.NextCursor
	}
	if pages < 2 {
		t.Errorf("expected the bound to split the index, got %d page(s)", pages)
	}
	for i := 1; i <= 60; i++ {
		if id := fmt.Sprintf("c%d", i); seen[id] != 1 {
			t.Errorf("row %s seen %d times", id, seen[id])
		}
	}
}

// A row larger than the bound still reaches the reader, so paging advances.
func TestPageHoldsOneRowOverTheBound(t *testing.T) {
	d := NewDigest(30)
	d.Signals.UserCorrections = []*Cluster{cluster(strings.Repeat("x", 200), "s1"), cluster("short", "s2")}
	d.assignIDs()
	data, err := IndexPage(d, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Rows) != 1 || idx.Page.NextCursor != 1 {
		t.Errorf("rows = %+v page = %+v", idx.Rows, idx.Page)
	}
}

// A listing's filter reads every occurrence, not only the normalized pattern.
func TestSignalPageMatchReadsOccurrences(t *testing.T) {
	d := sampleDigest()
	d.Signals.HookBlocks = append(d.Signals.HookBlocks, cluster("unrelated block", "s6"))
	d.Signals.HookBlocks[1].Occurrences[0].Text = "blocked: python3 -c used"
	d.assignIDs()

	data, err := SignalPage(d, "hook_blocks", regexp.MustCompile(`python3 -c`), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var l Listing
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Rows) != 1 || l.Rows[0].ID != "h2" {
		t.Errorf("rows = %+v, want only h2", l.Rows)
	}
	if _, err := SignalPage(d, "nope", nil, 0, 0); err == nil {
		t.Error("an unknown signal must be refused")
	}
}

// Details carry every occurrence of a cluster and every directive of a document.
func TestDetailPageHoldsEveryOccurrenceAndDirective(t *testing.T) {
	data, err := DetailPage(sampleDigest(), []string{"c1", "i1"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var det Detail
	if err := json.Unmarshal(data, &det); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, r := range det.Rows {
		got[r.ID] = append(got[r.ID], r.Session+r.Section)
	}
	if strings.Join(got["c1"], ",") != "s1,s2" || strings.Join(got["i1"], ",") != "Style,Git" {
		t.Errorf("rows = %+v", det.Rows)
	}
	if len(det.Items) != 2 || det.Items[0].Signal != "user_corrections" || det.Items[1].Path != "CLAUDE.md" {
		t.Errorf("items = %+v", det.Items)
	}
	if _, err := DetailPage(sampleDigest(), []string{"c9"}, 0, 0); err == nil {
		t.Error("an unknown id must be refused")
	}
}

// show reads the report the index came from, or refuses.
func TestLoadRefusesAReplacedReport(t *testing.T) {
	dir := t.TempDir()
	first := sampleDigest()
	if err := Save(dir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, first.ReportID); err != nil {
		t.Fatalf("the stored report must load by its own id: %v", err)
	}
	second := sampleDigest()
	second.WindowDays = 7
	if err := Save(dir, second); err != nil {
		t.Fatal(err)
	}
	if second.ReportID == first.ReportID {
		t.Fatal("two different reports got one id")
	}
	if _, err := Load(dir, first.ReportID); err == nil {
		t.Error("a replaced report must be refused")
	}
	got, err := Load(dir, "")
	if err != nil || got.WindowDays != 7 {
		t.Errorf("load without an id = %+v, %v", got, err)
	}
}

// A row's text is cut on a rune boundary.
func TestClipKeepsWholeRunes(t *testing.T) {
	s := strings.Repeat("é", 10) // two bytes each
	got := clip(s, 5)
	if !utf8.ValidString(got) || got != "éé" {
		t.Errorf("clip = %q", got)
	}
	if clip("ok", 5) != "ok" {
		t.Error("a short string must not change")
	}
}
