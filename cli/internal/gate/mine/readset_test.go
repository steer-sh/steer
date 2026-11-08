package mine

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// carryRead copies one state directory's read set into another, so a second corpus
// that extends the first is dumped against what a pass read of the first.
func carryRead(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(FindingsReadPath(from))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FindingsReadPath(to), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func markRead(t *testing.T, dir string) map[string]int {
	t.Helper()
	taken, err := MarkFindingsRead(dir)
	if err != nil {
		t.Fatal(err)
	}
	return taken
}

// sections splits an undecided dump at its read-before heading.
func sections(t *testing.T, out string) (string, string) {
	t.Helper()
	i := strings.Index(out, "\n"+readHeading+":")
	if i < 0 {
		t.Fatalf("the dump has no read-before section:\n%s", out)
	}
	return out[:i], out[i:]
}

func TestUndecidedPrintsAChainArrivedAfterTheReadFirst(t *testing.T) {
	first := findingsDir(t, nil, []map[string]any{c("an old one-off defect")})
	findingsOut(t, first, FindingsOpts{Kind: "accepted", Undecided: true})
	if taken := markRead(t, first); taken["accepted"] != 1 {
		t.Fatalf("mark-read must take the one shown finding; took %v", taken)
	}

	later := findingsDir(t, nil,
		[]map[string]any{c("an old one-off defect")},
		[]map[string]any{c("a later defect")})
	carryRead(t, first, later)

	out := findingsOut(t, later, FindingsOpts{Kind: "accepted", Undecided: true})
	newPart, readPart := sections(t, out)
	if !strings.Contains(newPart, "a later defect") || strings.Contains(newPart, "an old one-off defect") {
		t.Errorf("only the later chain belongs in the new section:\n%s", out)
	}
	if !strings.Contains(readPart, "an old one-off defect") {
		t.Errorf("the chain a pass read belongs in the read-before section:\n%s", out)
	}
}

func TestUndecidedReadsAFindingARetiredClassReturnsAsNew(t *testing.T) {
	covered := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")},
		[]map[string]any{c("a defect no class names")})
	findingsOut(t, covered, FindingsOpts{Kind: "accepted", Undecided: true})
	markRead(t, covered)

	retired := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "removed")},
		[]map[string]any{c("the parser drops a flag")},
		[]map[string]any{c("a defect no class names")})
	carryRead(t, covered, retired)

	newPart, readPart := sections(t, findingsOut(t, retired, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "the parser drops a flag") {
		t.Errorf("a finding a class covered at the read was never read, so it returns as new:\n%s", newPart)
	}
	if !strings.Contains(readPart, "a defect no class names") {
		t.Errorf("the finding the pass read stays read:\n%s", readPart)
	}
}

func TestANarrowedUndecidedDumpRecordsNothingAsShown(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("a defect no class names")})
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true, Since: 30})
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true, StrictJoins: true})
	if _, err := os.Stat(FindingsShownPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a dump that read part of the pile must not record a reading of all of it; stat: %v", err)
	}
	if taken := markRead(t, dir); len(taken) != 0 {
		t.Errorf("with no whole-pile dump there is nothing to take as read; took %v", taken)
	}
}

func TestUndecidedWithNoReadRecordedPutsEveryChainUnderNew(t *testing.T) {
	dir := findingsDir(t, nil,
		[]map[string]any{c("one defect")},
		[]map[string]any{c("another defect")})
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	newPart, readPart := sections(t, out)
	if !strings.Contains(newPart, "one defect") || !strings.Contains(newPart, "another defect") {
		t.Errorf("with no read recorded every chain is new:\n%s", out)
	}
	if strings.Contains(readPart, "---") {
		t.Errorf("with no read recorded the read-before section is empty:\n%s", readPart)
	}
	if !strings.Contains(out, "no earlier pass recorded a read") {
		t.Errorf("the header must say that no read is recorded:\n%s", out)
	}
}

func carryShown(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(FindingsShownPath(from))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FindingsShownPath(to), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAMarkAfterTwoDumpsTakesOnlyWhatBothShowed(t *testing.T) {
	early := findingsDir(t, nil, []map[string]any{c("an old one-off defect")})
	findingsOut(t, early, FindingsOpts{Kind: "accepted", Undecided: true})

	later := findingsDir(t, nil,
		[]map[string]any{c("an old one-off defect")},
		[]map[string]any{c("a defect that arrived between the dumps")})
	carryShown(t, early, later)
	findingsOut(t, later, FindingsOpts{Kind: "accepted", Undecided: true})
	if taken := markRead(t, later); taken["accepted"] != 1 {
		t.Fatalf("only the finding both dumps showed is read; took %v", taken)
	}

	newPart, _ := sections(t, findingsOut(t, later, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "a defect that arrived between the dumps") {
		t.Errorf("a finding the earlier pass never saw must stay new:\n%s", newPart)
	}
}

func TestAOneLedgerDumpRecordsNothingAsShown(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("a defect no class names")})
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true,
		Ledger: filepath.Join(dir, "adjudication.test.jsonl")})
	if _, err := os.Stat(FindingsShownPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a dump of one shard must not record a reading of the whole pile; stat: %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

func TestADumpThatFailedToWriteRecordsNothingAsShown(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("a defect no class names")})
	if err := Findings(failingWriter{}, dir, FindingsOpts{Kind: "accepted", Undecided: true}); err == nil {
		t.Fatal("a dump whose writes failed must report the error")
	}
	if _, err := os.Stat(FindingsShownPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a dump its reader did not receive must not record a reading; stat: %v", err)
	}
}

func TestUndecidedHeaderNamesAClassThatDecidesNothingHere(t *testing.T) {
	dir := findingsDir(t,
		[]Record{
			reviewRecord("live", "parser", "defect", "active"),
			reviewRecord("retired", "flag", "defect", "removed"),
			reviewRecord("noise", "style", "false-positive", "active"),
		},
		[]map[string]any{c("a defect no class names")})
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	for _, id := range []string{"retired", "noise"} {
		i := strings.Index(out, "id="+id)
		if i < 0 || !strings.Contains(out[i:i+200], "decides none of the findings below") {
			t.Errorf("class %s cannot cover an accepted finding and the header must say so:\n%s", id, out)
		}
	}
	block := out[strings.Index(out, "id=live"):]
	if end := strings.Index(block[1:], "id="); end >= 0 {
		block = block[:end+1]
	}
	if strings.Contains(block, "decides none") {
		t.Errorf("a live accepted class decides this view:\n%s", block)
	}
}

// readThenCover dumps and marks a corpus whose one finding no class covers yet,
// then adds an active class that covers it, as a pass that registers a class
// after its dump leaves the state.
func readThenCover(t *testing.T) string {
	t.Helper()
	dir := findingsDir(t, nil, []map[string]any{c("the parser drops a flag")})
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	markRead(t, dir)
	if err := Save(dir, []Record{reviewRecord("known", "parser", "defect", "active")}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRetiringAClassReturnsItsFindingsAsNew(t *testing.T) {
	dir := readThenCover(t)
	if err := Retire(io.Discard, dir, "known", "wrong tell", "removed"); err != nil {
		t.Fatal(err)
	}
	newPart, _ := sections(t, findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "the parser drops a flag") {
		t.Errorf("a finding a retired class stops covering must read as new:\n%s", newPart)
	}
}

func TestSupersedingAClassReturnsTheFindingsItStopsCoveringAsNew(t *testing.T) {
	dir := readThenCover(t)
	if err := Register(io.Discard, dir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Class: "narrower parser class", Match: "zzz-parser-only",
		Tier: "guard", HookID: "test-guard", Target: "steer-hooks.py", InstalledTS: 5000,
		Supersedes: "known", Force: true,
	}); err != nil {
		t.Fatalf("register refused the fixture: %v", err)
	}
	newPart, _ := sections(t, findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "the parser drops a flag") {
		t.Errorf("a finding the superseding class no longer covers must read as new:\n%s", newPart)
	}
}

func TestAnEmptyUndecidedDumpStillCarriesBothHeadings(t *testing.T) {
	dir := findingsDir(t,
		[]Record{reviewRecord("known", "parser", "defect", "active")},
		[]map[string]any{c("the parser drops a flag")})
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	if !strings.Contains(out, newHeading+": 0 chain(s)") || !strings.Contains(out, "\n"+readHeading+": 0 chain(s)") {
		t.Errorf("a reader reads up to the read-before heading, so an empty dump carries both:\n%s", out)
	}
}

func TestAClassRetiredBeforeTheMarkLeavesItsFindingsNew(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("the parser drops a flag")})
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	if err := Save(dir, []Record{reviewRecord("known", "parser", "defect", "active")}); err != nil {
		t.Fatal(err)
	}
	if err := Retire(io.Discard, dir, "known", "wrong tell", "removed"); err != nil {
		t.Fatal(err)
	}
	markRead(t, dir)
	newPart, _ := sections(t, findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "the parser drops a flag") {
		t.Errorf("a mark after the retirement must not take the returned finding as read:\n%s", newPart)
	}
}
