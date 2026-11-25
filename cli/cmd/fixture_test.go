package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoFindings is a reply in the shape internal/gate/prompts/review.md fixes,
// naming two different files.
const twoFindings = `[1] SEVERITY: BLOCKING
    FILE: cli/alpha.go
    CLAIM: the parser drops the last row.
    FAILS WHEN: the input ends without a newline.

[2] SEVERITY: SUBSTANTIVE
    FILE: cli/beta.go
    CLAIM: the writer never closes the file.
    FAILS WHEN: the process exits before the flush.
`

// twoFileDiff carries one hunk for each file the reply names, plus a third
// file no finding names.
const twoFileDiff = `diff --git a/cli/alpha.go b/cli/alpha.go
--- a/cli/alpha.go
+++ b/cli/alpha.go
@@ -1,2 +1,3 @@
 package main
+var alpha = 1
diff --git a/cli/beta.go b/cli/beta.go
--- a/cli/beta.go
+++ b/cli/beta.go
@@ -1,2 +1,3 @@
 package main
+var beta = 2
diff --git a/cli/gamma.go b/cli/gamma.go
--- a/cli/gamma.go
+++ b/cli/gamma.go
@@ -1,2 +1,3 @@
 package main
+var gamma = 3
`

// mineFindings runs the finding-narrowed miner over a temporary diff and
// returns the decoded output.
func mineFindings(t *testing.T, diff, finding string) map[string]any {
	t.Helper()
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "row.diff")
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.json")
	prev := fixtureOut
	fixtureOut = outPath
	defer func() { fixtureOut = prev }()

	if err := mineDiffByFindings(diffPath, finding); err != nil {
		t.Fatalf("mineDiffByFindings: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func findingGroups(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["findings"].([]any)
	if !ok {
		t.Fatalf("output carries no findings: %v", out)
	}
	groups := make([]map[string]any, 0, len(raw))
	for _, g := range raw {
		groups = append(groups, g.(map[string]any))
	}
	return groups
}

func TestEachFindingNarrowsToItsOwnFile(t *testing.T) {
	groups := findingGroups(t, mineFindings(t, twoFileDiff, twoFindings))
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want one per finding", len(groups))
	}
	want := []string{"cli/alpha.go", "cli/beta.go"}
	for i, group := range groups {
		if group["file"] != want[i] {
			t.Errorf("group %d names %v, want %s", i, group["file"], want[i])
		}
		candidates := group["candidates"].([]any)
		if len(candidates) != 1 {
			t.Fatalf("group %d holds %d candidates, want the one hunk of its own file", i, len(candidates))
		}
		row := candidates[0].(map[string]any)
		if row["path"] != want[i] {
			t.Errorf("group %d carries a candidate for %v, want %s", i, row["path"], want[i])
		}
		source := row["source"].(map[string]any)
		if source["finding_index"].(float64) != float64(i+1) {
			t.Errorf("group %d candidate names finding %v, want %d", i, source["finding_index"], i+1)
		}
		if source["diff_file"] != "row.diff" {
			t.Errorf("group %d candidate names diff %v, want row.diff", i, source["diff_file"])
		}
	}
}

func TestAMalformedFindingIsAnErrorRatherThanEveryHunk(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "row.diff")
	if err := os.WriteFile(diffPath, []byte(twoFileDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := "[1] SEVERITY: BLOCKING\n    CLAIM: the fields are out of order.\n    FILE: cli/alpha.go\n    FAILS WHEN: always.\n"
	err := mineDiffByFindings(diffPath, broken)
	if err == nil {
		t.Fatal("a finding block the parser cannot read must be an error, never every hunk in the diff")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error names no line: %v", err)
	}
}

func TestFindingAndPathRefuseEachOther(t *testing.T) {
	prevDiff, prevPath, prevFinding := fixtureMineDiff, fixtureMinePath, fixtureMineFinding
	defer func() {
		fixtureMineDiff, fixtureMinePath, fixtureMineFinding = prevDiff, prevPath, prevFinding
	}()
	fixtureMineDiff = "row.diff"
	fixtureMinePath = "cli/alpha.go"
	fixtureMineFinding = twoFindings

	err := fixtureMineCmd.RunE(fixtureMineCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("got %v, want a refusal naming both flags", err)
	}
}

func TestAFindingNamingADroppedFileYieldsNoCandidates(t *testing.T) {
	dropped := "[The review harness dropped 1 file to fit its budget]\n" + `diff --git a/cli/beta.go b/cli/beta.go
--- a/cli/beta.go
+++ b/cli/beta.go
@@ -1,2 +1,3 @@
 package main
+var beta = 2
`
	groups := findingGroups(t, mineFindings(t, dropped, twoFindings))
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want one per finding", len(groups))
	}
	if groups[0]["complete"] != false {
		t.Error("the finding naming the dropped file reads as complete")
	}
	if n := len(groups[0]["candidates"].([]any)); n != 0 {
		t.Errorf("the dropped file yielded %d candidates, want none", n)
	}
	if reason, _ := groups[0]["reason"].(string); !strings.Contains(reason, "cli/alpha.go") {
		t.Errorf("the group names no reason for the missing file: %q", reason)
	}
	if groups[1]["complete"] != true {
		t.Error("a file the diff does carry must still yield its candidates")
	}
}

func TestAFindingCitationCarriesALineNumber(t *testing.T) {
	cited := "[1] SEVERITY: BLOCKING\n    FILE: `cli/alpha.go:12-18`\n    CLAIM: one.\n    FAILS WHEN: always.\n"
	groups := findingGroups(t, mineFindings(t, twoFileDiff, cited))
	if groups[0]["file"] != "cli/alpha.go" {
		t.Fatalf("the citation was not normalised: %v", groups[0]["file"])
	}
	if len(groups[0]["candidates"].([]any)) != 1 {
		t.Error("a decorated citation narrowed to no hunk")
	}
}
