package reviewers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func toolRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", ".")
	for p, body := range files {
		writeRepoFile(t, root, p, body)
	}
	run("add", "-A")
	return root
}

func writeRepoFile(t *testing.T, root, p, body string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolsFor(t *testing.T, root string) *viewTools {
	t.Helper()
	view, ok := gate.NewCommitView(gate.Defaults(), root, gate.DiffSpec{StagedOnly: true})
	if !ok {
		t.Fatal("NewCommitView failed")
	}
	return &viewTools{view: view}
}

func callTool(t *testing.T, tools *viewTools, name string, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, refused := tools.call(context.Background(), name, string(raw))
	if refused {
		t.Fatalf("%s refused: %s", name, out)
	}
	return out
}

// TestReadFileReadsTheCommitNotTheDisk: an unstaged edit that does not land is not
// what read_file returns.
func TestReadFileReadsTheCommitNotTheDisk(t *testing.T) {
	root := toolRepo(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	writeRepoFile(t, root, "a.go", "unstaged\n")
	out := callTool(t, toolsFor(t, root), "read_file", map[string]any{"path": "a.go", "start_line": 2, "end_line": 3})
	if !strings.Contains(out, "lines 2-3 of 3") || !strings.Contains(out, "     2\ttwo\n") ||
		!strings.Contains(out, "     3\tthree\n") || strings.Contains(out, "one") {
		t.Errorf("read_file = %q", out)
	}
	if strings.Contains(out, "unstaged") {
		t.Errorf("read_file returned the unstaged disk content: %q", out)
	}
}

// TestReadFileRefusesAPathOutsideTheView: a path the commit does not land, such as
// one that climbs out of the repository, is an error result, not a disk read.
func TestReadFileRefusesAPathOutsideTheView(t *testing.T) {
	root := toolRepo(t, map[string]string{"a.go": "a\n"})
	writeRepoFile(t, root, "untracked.go", "secret\n")
	tools := toolsFor(t, root)
	for _, p := range []string{"../a.go", "/etc/hosts", "untracked.go"} {
		out := callTool(t, tools, "read_file", map[string]any{"path": p})
		if !strings.Contains(out, "is not a file in this commit") {
			t.Errorf("read_file(%s) = %q", p, out)
		}
	}
}

// TestGrepIsFixedStringOverEveryFile: grep scans index objects in one batch and treats
// the pattern literally.
func TestGrepIsFixedStringOverEveryFile(t *testing.T) {
	root := toolRepo(t, map[string]string{
		"a.go": "x := a.b\n", "b.go": "y := axb\n", "c/d.go": "z := a.b // twice a.b\n",
		"e.txt": "a.b in prose\n",
	})
	out := callTool(t, toolsFor(t, root), "grep", map[string]any{"pattern": "a.b", "path_glob": "*.go"})
	for _, want := range []string{"a.go:1: x := a.b", "c/d.go:1: z := a.b // twice a.b"} {
		if !strings.Contains(out, want) {
			t.Errorf("grep = %q; missing %q", out, want)
		}
	}
	if strings.Contains(out, "b.go") || strings.Contains(out, "e.txt") {
		t.Errorf("grep = %q; matched a regex or a file outside the glob", out)
	}
}

// TestGrepStopsAtTheMatchCap: a common string returns at most maxGrepMatches lines
// and says that it stopped.
func TestGrepStopsAtTheMatchCap(t *testing.T) {
	root := toolRepo(t, map[string]string{"a.go": strings.Repeat("hit\n", maxGrepMatches+5)})
	out := callTool(t, toolsFor(t, root), "grep", map[string]any{"pattern": "hit"})
	if got := strings.Count(out, "a.go:"); got != maxGrepMatches {
		t.Errorf("grep returned %d matches, want %d", got, maxGrepMatches)
	}
	if !strings.Contains(out, "stopped at") {
		t.Errorf("grep = %q; does not say it stopped", out)
	}
}

func TestListFilesGlobForms(t *testing.T) {
	root := toolRepo(t, map[string]string{"cli/a.go": "", "cli/sub/b.go": "", "docs/c.md": ""})
	tools := toolsFor(t, root)
	cases := map[string]string{
		"":         "cli/a.go\ncli/sub/b.go\ndocs/c.md\n",
		"cli/":     "cli/a.go\ncli/sub/b.go\n",
		"cli/*.go": "cli/a.go\n",
		"*.go":     "cli/a.go\ncli/sub/b.go\n",
	}
	for glob, want := range cases {
		if got := callTool(t, tools, "list_files", map[string]any{"path_glob": glob}); got != want {
			t.Errorf("list_files(%q) = %q, want %q", glob, got, want)
		}
	}
}

// TestToolOutputBudgetRefusesOnceSpent: each result is cut at maxCallBytes, and once
// the run has returned maxRunToolBytes every call is refused.
func TestToolOutputBudgetRefusesOnceSpent(t *testing.T) {
	root := toolRepo(t, map[string]string{"big.txt": strings.Repeat("0123456789abcdef\n", 4000)})
	tools := toolsFor(t, root)
	raw := `{"path":"big.txt"}`
	for maxRunToolBytes-tools.bytes > 2*cutNoteBytes {
		out, refused := tools.call(context.Background(), "read_file", raw)
		if refused {
			t.Fatalf("refused at %d bytes, before the budget", tools.bytes)
		}
		if len(out) > maxCallBytes+64 {
			t.Fatalf("one result is %d bytes, over the per-call cap", len(out))
		}
	}
	if tools.bytes > maxRunToolBytes+64 {
		t.Errorf("the run returned %d bytes, over the run cap", tools.bytes)
	}
	if _, refused := tools.call(context.Background(), "read_file", raw); !refused {
		t.Error("a call after the budget was spent was not refused")
	}
}

func TestUnknownToolAndBadArgumentsAreResults(t *testing.T) {
	tools := toolsFor(t, toolRepo(t, map[string]string{"a.go": "a\n"}))
	if out, _ := tools.call(context.Background(), "write_file", `{}`); !strings.Contains(out, "no tool named") {
		t.Errorf("unknown tool = %q", out)
	}
	if out, _ := tools.call(context.Background(), "read_file", `not json`); !strings.Contains(out, "not a JSON object") {
		t.Errorf("bad arguments = %q", out)
	}
}

// TestReadFileKeepsItsContinuationWithinTheCap: a long file stops at a whole line with
// the line to read on from, and a single overlong line names its size; both stay
// inside the per-call cap.
func TestReadFileKeepsItsContinuationWithinTheCap(t *testing.T) {
	root := toolRepo(t, map[string]string{
		"long.txt": strings.Repeat("0123456789abcdef\n", 4000),
		"wide.txt": strings.Repeat("x", 3*maxCallBytes) + "\nnext\n",
	})
	tools := toolsFor(t, root)
	out := callTool(t, tools, "read_file", map[string]any{"path": "long.txt"})
	if len(out) > maxCallBytes || !strings.Contains(out, "read on with start_line=") {
		t.Errorf("long.txt: %d bytes, continuation present %v", len(out), strings.Contains(out, "read on"))
	}
	out = callTool(t, tools, "read_file", map[string]any{"path": "wide.txt"})
	if len(out) > maxCallBytes || !strings.Contains(out, "line 1 is 73728 bytes") ||
		!strings.Contains(out, "start_line=2") {
		t.Errorf("wide.txt: %d bytes, tail %q", len(out), out[max(0, len(out)-160):])
	}
}

// TestGrepNamesFilesItCouldNotRead: a file that left the disk after the view was built
// is reported as unread, not as binary or large.
func TestGrepNamesFilesItCouldNotRead(t *testing.T) {
	root := toolRepo(t, map[string]string{"a.go": "a\n"})
	writeRepoFile(t, root, "new.go", "needle\n")
	view, ok := gate.NewCommitView(gate.Defaults(), root, gate.DiffSpec{IncludeUntracked: true})
	if !ok {
		t.Fatal("NewCommitView failed")
	}
	if err := os.Remove(filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, &viewTools{view: view}, "grep", map[string]any{"pattern": "needle"})
	if !strings.Contains(out, "could not read 1 files, not searched: new.go") || strings.Contains(out, "binary or large") {
		t.Errorf("grep = %q", out)
	}
}

// TestGrepShowsAMatchFarIntoALongLine: the window around a match deep in a long line
// holds the match.
func TestGrepShowsAMatchFarIntoALongLine(t *testing.T) {
	root := toolRepo(t, map[string]string{"a.js": strings.Repeat("x", 5000) + "NEEDLE" + strings.Repeat("y", 5000) + "\n"})
	out := callTool(t, toolsFor(t, root), "grep", map[string]any{"pattern": "NEEDLE"})
	if !strings.Contains(out, "a.js:1: …") || !strings.Contains(out, "NEEDLE") || len(out) > 2*maxLineChars {
		t.Errorf("grep = %q", out)
	}
}

// TestGrepKeepsItsNotesInsideTheCap: matches that fill the room leave the notes on
// how complete the search was, inside the per-call cap.
func TestGrepKeepsItsNotesInsideTheCap(t *testing.T) {
	files := map[string]string{"bin.dat": "hit\x00"}
	for i := range 80 {
		files[fmt.Sprintf("dir%02d/%s.go", i, strings.Repeat("p", 150))] = "hit " + strings.Repeat("z", 190) + "\n"
	}
	out := callTool(t, toolsFor(t, toolRepo(t, files)), "grep", map[string]any{"pattern": "hit"})
	if len(out) > maxCallBytes || !strings.Contains(out, "stopped at the output limit") ||
		!strings.Contains(out, "skipped 1 binary") {
		t.Errorf("grep: %d bytes, tail %q", len(out), out[max(0, len(out)-300):])
	}
}

// TestCallRefusesARemainderTooSmallForItsNote: a nearly spent budget refuses rather
// than cut a result to a negative length.
func TestCallRefusesARemainderTooSmallForItsNote(t *testing.T) {
	tools := toolsFor(t, toolRepo(t, map[string]string{"a.go": strings.Repeat("a\n", 1000)}))
	tools.bytes = maxRunToolBytes - 10
	if _, refused := tools.call(context.Background(), "read_file", `{"path":"a.go"}`); !refused {
		t.Error("a call with 10 bytes of budget left was not refused")
	}
}

// TestReadFileKeepsASpaceThatIsPartOfTheName: a tracked name that begins with a space
// reads as given, not as the trimmed name of another file.
func TestReadFileKeepsASpaceThatIsPartOfTheName(t *testing.T) {
	root := toolRepo(t, map[string]string{" report.txt": "spaced\n", "report.txt": "plain\n"})
	tools := toolsFor(t, root)
	if out := callTool(t, tools, "read_file", map[string]any{"path": " report.txt"}); !strings.Contains(out, "spaced") {
		t.Errorf("read_file(' report.txt') = %q", out)
	}
	if out := callTool(t, tools, "read_file", map[string]any{"path": "./report.txt "}); !strings.Contains(out, "plain") {
		t.Errorf("read_file('./report.txt ') = %q", out)
	}
}

// TestListFilesKeepsItsCountInsideTheCap: paths that fill the room before the path
// cap end with how many were left out.
func TestListFilesKeepsItsCountInsideTheCap(t *testing.T) {
	files := map[string]string{}
	for i := range 300 {
		files[fmt.Sprintf("%03d-%s.go", i, strings.Repeat("n", 120))] = ""
	}
	out := callTool(t, toolsFor(t, toolRepo(t, files)), "list_files", map[string]any{})
	if len(out) > maxCallBytes || !strings.Contains(out, " more; narrow path_glob]") {
		t.Errorf("list_files: %d bytes, tail %q", len(out), out[max(0, len(out)-120):])
	}
}

// TestGrepDoesNotDenyAMatchItHadNoRoomFor: a match too wide for the room is reported
// as stopped at the limit, never as no match.
func TestGrepDoesNotDenyAMatchItHadNoRoomFor(t *testing.T) {
	tools := toolsFor(t, toolRepo(t, map[string]string{"a.go": "hit\n"}))
	out, err := tools.grep(context.Background(), "hit", "", grepNoteBytes+2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no line holds") || !strings.Contains(out, "stopped at the output limit") {
		t.Errorf("grep = %q", out)
	}
}
