package cc

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// TestAbsoluteRepoCommitKeepsAStrayOutOfTheReview runs the command shape that leaked
// an untracked plan file to three reviewers: add and commit both select the
// repository with `git -C <absolute path>`.
func TestAbsoluteRepoCommitKeepsAStrayOutOfTheReview(t *testing.T) {
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
	write := func(path string) {
		t.Helper()
		body := strings.Repeat("line x\n", 80)
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old.go")
	run("init", "-q", ".")
	run("add", "old.go")
	run("co"+"mmit", "-qm", "init")
	write("new_test.go")
	write("check_plan.md")

	cmd := "git -C " + root + " add new_test.go && git -C " + root + " co" + "mmit -F msg"
	diff, ok := gate.GetDiff(gate.Defaults(), RepoDir(cmd, "/elsewhere"), DiffSpecFor(cmd, "/elsewhere"))
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(diff, "new_test.go") {
		t.Error("the file the add names must reach the reviewer")
	}
	if strings.Contains(diff, "check_plan.md") {
		t.Error("an untracked file no add names cannot land in this commit")
	}
}

// TestABareAddInTheShellDirectoryKeepsAStrayOutOfTheReview runs the form Claude Code
// hands the hook when it drops a leading `cd` to its own working directory: a bare
// add, then a commit that selects the same repository with `git -C`.
func TestABareAddInTheShellDirectoryKeepsAStrayOutOfTheReview(t *testing.T) {
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
	for _, path := range []string{"old.go", "new_test.go", "check_plan.md"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(strings.Repeat("line x\n", 80)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", ".")
	run("add", "old.go")
	run("co"+"mmit", "-qm", "init")

	cmd := "git add new_test.go && git -C " + root + " co" + "mmit -F msg"
	diff, ok := gate.GetDiff(gate.Defaults(), RepoDir(cmd, root), DiffSpecFor(cmd, root))
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(diff, "new_test.go") {
		t.Error("the file the add names must reach the reviewer")
	}
	if strings.Contains(diff, "check_plan.md") {
		t.Error("an untracked file no add names cannot land in this commit")
	}
}

func TestStagedPathspecs(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
		why  string
	}{
		{"git add cli/cmd && git commit -F -", []string{"cli/cmd"},
			"the reported defect: one subtree staged, the whole repo reviewed"},
		{"git add a.go b.go && git commit -m x", []string{"a.go", "b.go"}, "every named path"},
		{"git add -- -weird.go && git commit -m x", []string{"-weird.go"},
			"past `--` a leading dash is a path, not an option"},
		{"git add 'my file.go' && git commit -m x", []string{"my file.go"},
			"a quoted path holding a space is one token"},
		{"git add cli && git add docs && git commit -m x", []string{"cli", "docs"},
			"two staging steps both name paths"},
		{"git add . && git commit -m x", []string{"."}, "`.` is a pathspec git resolves itself"},
		{"git add -A && git commit -m x", nil, "no pathspec means the whole repo"},
		{"git add && git commit -m x", nil, "no pathspec means the whole repo"},
		{"git add -A cli && git commit -m x", []string{"cli"}, "-A still obeys a pathspec"},
		{"git add $FILES && git commit -m x", nil, "a variable expands to text this does not have"},
		{"git add \"$(ls)\" && git commit -m x", nil, "a substitution expands to text this does not have"},
		{"git add --pathspec-from-file=list && git commit -m x", nil,
			"the paths live in a file, so the command text does not name them"},
		{"git -C /other add cli && git commit -m x", nil,
			"the paths resolve against a directory the plan does not read"},
		{"git -C /r add a.go b.go && git -C /r commit -F msg", []string{"a.go", "b.go"},
			"the form the gate's own refusal asks for: add and commit share one directory"},
		{"git -C /r add a.go && git -C /s commit -m x", nil, "add and commit name different repositories"},
		{"git add a.go && git -C /w commit -F msg", []string{"a.go"},
			"a bare add runs in the shell's directory, and -C names that same directory"},
		{"git add a.go && git -C /r commit -F msg", nil,
			"a bare add runs in the shell's directory, and -C names another"},
		{"cd /r && git add a.go && git commit -m x", []string{"a.go"}, "one cd moves both"},
		{"git -C /r add a.go && cd /r && git commit -m x", []string{"a.go"},
			"cd and -C that land in one directory"},
		{"git add a.go && cd sub && git commit -m x", nil,
			"the paths resolve against the directory above the commit's"},
		{"git -C sub add a.go && git -C sub commit -m x", []string{"a.go"},
			"a relative -C that both calls share"},
		{"git commit -am x", nil, "no staging step at all"},
		{"git add cli; git commit -m x", []string{"cli"}, "`;` separates, it does not join"},
	}
	for _, tc := range cases {
		got := StagedPathspecs(tc.cmd, "/w")
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("StagedPathspecs(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

func TestDiffSpecForCarriesPathspecsOnlyWithUntracked(t *testing.T) {
	spec := DiffSpecFor("git add cli/cmd && git com"+"mit -F -", "/w")
	if spec.StagedOnly || !spec.IncludeUntracked {
		t.Fatalf("a staging step reads the working tree and opts into untracked files: %+v", spec)
	}
	if !reflect.DeepEqual(spec.UntrackedPathspecs, []string{"cli/cmd"}) {
		t.Errorf("UntrackedPathspecs = %v", spec.UntrackedPathspecs)
	}
	if !reflect.DeepEqual(spec.TrackedPathspecs, []string{"cli/cmd"}) {
		t.Errorf("TrackedPathspecs = %v", spec.TrackedPathspecs)
	}
	// -a stages tracked files only, so there is no untracked half to narrow.
	bare := DiffSpecFor("git com"+"mit -am x", "/w")
	if bare.IncludeUntracked || bare.UntrackedPathspecs != nil || bare.TrackedPathspecs != nil {
		t.Errorf("commit -a: %+v", bare)
	}
	// The one form where the two halves disagree: the add bounds which NEW files can
	// land, while -a lands every tracked modification, named or not.
	both := DiffSpecFor("git add cli/cmd && git com"+"mit -am x", "/w")
	if !reflect.DeepEqual(both.UntrackedPathspecs, []string{"cli/cmd"}) {
		t.Errorf("add + commit -a: UntrackedPathspecs = %v", both.UntrackedPathspecs)
	}
	if both.TrackedPathspecs != nil {
		t.Errorf("add + commit -a: narrowing the tracked half hides a file -a stages: %v",
			both.TrackedPathspecs)
	}
}
