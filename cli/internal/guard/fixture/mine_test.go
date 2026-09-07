package fixture

import "testing"

const multiHunkDiff = `diff --git a/cli/main.go b/cli/main.go
index 1111111..2222222 100644
--- a/cli/main.go
+++ b/cli/main.go
@@ -1,3 +1,4 @@
 package main
+import "os"
 
 func main() {
@@ -40,4 +41,5 @@ func run() {
 	work()
+	exec.Command("python", "x.py")
 }
diff --git a/README.md b/README.md
index 3333333..4444444 100644
--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 # steer
+A line.
`

func TestEachHunkIsItsOwnCandidate(t *testing.T) {
	hunks, trunc, err := ParseDiff(multiHunkDiff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if trunc.Lossy() {
		t.Fatalf("a complete diff reads as lossy: %+v", trunc)
	}
	if len(hunks) != 3 {
		t.Fatalf("want 3 hunks, got %d", len(hunks))
	}
	if hunks[0].Path != "cli/main.go" || hunks[0].Index != 1 {
		t.Fatalf("first hunk is %s #%d", hunks[0].Path, hunks[0].Index)
	}
	if hunks[1].Index != 2 {
		t.Fatalf("second hunk of the same file is #%d", hunks[1].Index)
	}
	// Per-hunk is what keeps an import at line 1 out of a change at line 41.
	first := hunks[0].Payload["tool_input"].(map[string]any)["new_string"].(string)
	if first != "import \"os\"\n" {
		t.Fatalf("first hunk content is %q", first)
	}
	second := hunks[1].Payload["tool_input"].(map[string]any)["new_string"].(string)
	if second != "\texec.Command(\"python\", \"x.py\")\n" {
		t.Fatalf("second hunk content is %q", second)
	}
}

func TestTheFindingsFileNarrowsCandidatesAndDropsTheRest(t *testing.T) {
	hunks, _, err := ParseDiff(multiHunkDiff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	selected := HunksFor(hunks, "cli/main.go")
	if len(selected) != 2 {
		t.Fatalf("want the 2 hunks of the named file, got %d", len(selected))
	}
	for _, h := range selected {
		if h.Path != "cli/main.go" {
			t.Fatalf("a hunk of %s survived the narrowing", h.Path)
		}
	}
}

func TestANewFileIsAWriteCall(t *testing.T) {
	diff := `diff --git a/new.py b/new.py
new file mode 100644
index 0000000..5555555
--- /dev/null
+++ b/new.py
@@ -0,0 +1,2 @@
+import os
+print(os.getcwd())
`
	hunks, _, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(hunks) != 1 || !hunks[0].NewFile {
		t.Fatalf("want one new-file hunk, got %+v", hunks)
	}
	if hunks[0].Payload["tool_name"] != "Write" {
		t.Fatalf("a new file is %v", hunks[0].Payload["tool_name"])
	}
	input := hunks[0].Payload["tool_input"].(map[string]any)
	if input["file_path"] != "new.py" {
		t.Fatalf("payload carries file_path %v", input["file_path"])
	}
	if input["content"] != "import os\nprint(os.getcwd())\n" {
		t.Fatalf("payload content is %q", input["content"])
	}
}

func TestADeletionOnlyHunkYieldsNoFixture(t *testing.T) {
	diff := `diff --git a/old.py b/old.py
index 6666666..7777777 100644
--- a/old.py
+++ b/old.py
@@ -1,3 +1,1 @@
 keep
-import os
-import sys
`
	hunks, _, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("want one hunk, got %d", len(hunks))
	}
	if hunks[0].Skip == "" {
		t.Fatal("a deletion-only hunk built a payload; a guard never saw those lines live")
	}
	if hunks[0].Payload != nil {
		t.Fatal("a skipped hunk carries a payload")
	}
}

func TestBothBudgetPreamblesReadAsLossy(t *testing.T) {
	dropped := "[The review harness dropped 2 file(s) from the diff below to fit its budget, NOT the commit: a, b. Full stat:]\n\nstat\n\n" + multiHunkDiff
	hunks, trunc, err := ParseDiff(dropped)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if !trunc.DroppedFiles || trunc.StatOnly {
		t.Fatalf("dropped-files preamble read as %+v", trunc)
	}
	// Budgeting keeps whole file sections, so a file that is present is whole.
	if !Complete(hunks, trunc, "cli/main.go") {
		t.Fatal("a file whose hunks are present read as incomplete")
	}
	if Complete(hunks, trunc, "gone.go") {
		t.Fatal("a dropped file read as complete")
	}

	statOnly := "[diff is 900000 chars — too large to inline, and no single file's diff fits the budget.]\n\nstat\n"
	_, trunc, err = ParseDiff(statOnly)
	if err != nil {
		t.Fatalf("ParseDiff on a stat-only diff: %v", err)
	}
	if !trunc.StatOnly {
		t.Fatalf("stat-only preamble read as %+v", trunc)
	}
	if Complete(nil, trunc, "cli/main.go") {
		t.Fatal("a stat-only diff read as complete")
	}
}
