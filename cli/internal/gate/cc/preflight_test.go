package cc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreflightDenyRefusesACommitThatSelectsItsOwnPaths: `git commit <paths>` lands
// those paths from the working tree and --include adds them to the index, while the
// gate reviews the index alone.
func TestPreflightDenyRefusesACommitThatSelectsItsOwnPaths(t *testing.T) {
	c := "co" + "mmit"
	for _, cmd := range []string{
		"git " + c + " -m x file.go",
		"git " + c + " -m x -- file.go",
		"git " + c + " --only -m x file.go",
		"git " + c + " -o -m x",
		"git " + c + " --include -m x",
		"git " + c + " -im x",
		"git " + c + " --pathspec-from-file=list -m x",
		"git " + c + " --pathspec-from-file list -m x",
		`git ` + c + ` -m x "$FILE"`,
		`git ` + c + ` -Sm "release message"`,
	} {
		if reason, receipt := PreflightDeny(cmd, "/cwd"); receipt != SelectedPathsReceipt {
			t.Errorf("%q selects its own paths and must be refused, got %q", cmd, reason)
		}
	}
	for _, cmd := range []string{
		"git " + c + " -m x",
		"git " + c + " -qm x",
		"git " + c + " -am x",
		"git " + c + " -F msg",
		"git " + c + " -m x --",
		"git " + c + " -S -m x",
		"git " + c + " -C HEAD",
		"git " + c + ` --author "A <a@b>" -m x`,
		"git " + c + ` -m "gate: refuse -i and file.go"`,
		"git " + c + " -uno -m x",
		"git " + c + ` --author="$A" -m x`,
	} {
		if reason, _ := PreflightDeny(cmd, "/cwd"); reason != "" {
			t.Errorf("%q commits the index and must go to review, got %q", cmd, reason)
		}
	}
	if CommitsAllTracked("git " + c + " -uall -m x") {
		t.Error("-uall sets the untracked-files mode; its `a` is not -a")
	}
}

// TestPreflightDenyRefusesABundledAddOverAPartlyStagedFile: a bundled add is reviewed
// from the working tree, so a file with staged and unstaged edits would reach the
// reviewers with edits the commit does not carry.
func TestPreflightDenyRefusesABundledAddOverAPartlyStagedFile(t *testing.T) {
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
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := strings.Repeat("line x\n", 80)
	write("code.go", base)
	write("ARCH.md", base)
	run("init", "-q", ".")
	run("add", ".")
	run("co"+"mmit", "-qm", "init")
	write("code.go", base+"changed\n")

	bundled := "git add code.go && git -C " + root + " co" + "mmit -m x"
	if reason, _ := PreflightDeny(bundled, root); reason != "" {
		t.Fatalf("no file is partly staged, so the bundle must go to review: %s", reason)
	}

	write("ARCH.md", base+"staged paragraph\n")
	run("add", "ARCH.md")
	write("ARCH.md", base+"staged paragraph\nunstaged paragraph\n")

	reason, receipt := PreflightDeny(bundled, root)
	if !strings.Contains(reason, "ARCH.md") {
		t.Errorf("the refusal must name the partly staged file, got %q", reason)
	}
	if receipt != PartlyStagedReceipt {
		t.Errorf("the receipt must say no review ran, got %q", receipt)
	}

	for _, cmd := range []string{
		"git -C " + root + " co" + "mmit -m x",
		"git add code.go && git -C " + root + " co" + "mmit -am x",
	} {
		if reason, _ := PreflightDeny(cmd, root); reason != "" {
			t.Errorf("%q reviews the tree it lands, so it must not be refused: %s", cmd, reason)
		}
	}
}

// TestPreflightDenyRefusesAStepThatRunsBeforeTheCommit: the gate reads the tree before
// the command starts, so an edit the same command makes before git commits lands
// unreviewed.
func TestPreflightDenyRefusesAStepThatRunsBeforeTheCommit(t *testing.T) {
	c := "co" + "mmit"
	for _, cmd := range []string{
		"python3 edit.py && git add a && git " + c + " -m x",
		"python3 - <<'EOF'\nopen('a', 'w')\nEOF\ngit add a && git " + c + " -m x",
		"sed -i s/a/b/ f && git add f && git " + c + " -m x",
		"git stash && git " + c + " -m x",
		"echo x > a && git add a && git " + c + " -m x",
		"{ echo x; } > a; git " + c + " -m x",
		"echo x >& a; git " + c + " -m x",
		`git ` + c + ` -m x --author="$(touch a; echo A)"`,
		`git ` + c + ` -m x <<< "$(touch a)"`,
	} {
		reason, receipt := PreflightDeny(cmd, "/cwd")
		if receipt != StepBeforeCommitReceipt {
			t.Errorf("%q changes the tree before git commits and must be refused, got %q", cmd, reason)
		}
	}
	for _, cmd := range []string{
		"git " + c + " -m x",
		"cd /repo && git add a b && git " + c + " -m x",
		"git -C /repo add -A && git -C /repo " + c + " -m x",
		"git add a 2>/dev/null && git " + c + " -m x",
		"git status --short && git " + c + " -m x",
		"git " + c + " -F - <<'EOF'\nsubject\n\nbody\nEOF",
		"git " + c + " -m x && git log --oneline -1 && python3 after.py > out.txt",
		"git " + c + " -F - <<'EOF' && git status -sb | head -2 && python3 after.py\nsubject\nEOF",
		"cat > /tmp/msg <<'EOF'\nsubject\nEOF\ngit " + c + " -F /tmp/msg",
	} {
		if reason, _ := PreflightDeny(cmd, "/cwd"); reason != "" {
			t.Errorf("%q changes nothing before git commits and must go to review, got %q", cmd, reason)
		}
	}
}

// TestPreflightDenyRefusesARedirectOnTheCommitItself: the shell opens a commit's own
// output redirect before git reads the tree, so `git commit -a > code.go` lands
// code.go empty after the reviewers read it whole.
func TestPreflightDenyRefusesARedirectOnTheCommitItself(t *testing.T) {
	c := "co" + "mmit"
	for cmd, redir := range map[string]string{
		"git " + c + " -a -m x > code.go":                    "> code.go",
		"git " + c + " -m x > out.log":                       "> out.log",
		"git " + c + " --all -m x 2> code.go":                "2> code.go",
		"git " + c + " -am x &> code.go":                     "&> code.go",
		"git " + c + " -am x >| code.go":                     ">| code.go",
		"git " + c + " -am x >> code.go":                     ">> code.go",
		"git " + c + " -am x >& code.go":                     ">& code.go",
		"git " + c + " -am x 2>&1 >code.go":                  ">code.go",
		`git ` + c + ` -am x > "$OUT"`:                       `> "$OUT"`,
		"git add a && git " + c + " -m x > a":                "> a",
		"git " + c + " -F - > code.go <<'EOF'\nsubject\nEOF": "> code.go",
	} {
		reason, receipt := PreflightDeny(cmd, "/cwd")
		if receipt != StepBeforeCommitReceipt || reason != fmt.Sprintf(commitRedirectReason, redir) {
			t.Errorf("%q writes %q before git reads the tree and must be refused naming it, got %q", cmd, redir, reason)
		}
	}
	for _, cmd := range []string{
		"git " + c + " -am x > /dev/null",
		"git " + c + " -am x 2>/dev/null",
		"git " + c + " -am x > /tmp/commit.log 2>&1",
		"git " + c + " -am x 2>&1",
		"git " + c + " -am x >&2",
		"git " + c + " -am x 2>&-",
		"git " + c + " -F - <<'EOF'\nsubject\nEOF",
	} {
		if reason, _ := PreflightDeny(cmd, "/cwd"); reason != "" {
			t.Errorf("%q writes no file git reads and must go to review, got %q", cmd, reason)
		}
	}
}

// TestPreflightDenyJudgesAScratchPathAgainstTheResolvedRepository: a scratch root can
// hold the repository itself, and a link under one can point into it, so a redirect
// target is scratch only when it lies outside the repository's top level with its
// links resolved.
func TestPreflightDenyJudgesAScratchPathAgainstTheResolvedRepository(t *testing.T) {
	c := "co" + "mmit"
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	sub := filepath.Join(repo, "sub")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link, dangling := filepath.Join(tmp, "link"), filepath.Join(tmp, "dangling")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(repo, "new.go"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		filepath.Join(repo, "code.go"),
		filepath.Join(link, "code.go"),
		dangling,
	} {
		cmd := "git " + c + " -am x > " + target
		if reason, receipt := PreflightDeny(cmd, sub); receipt != StepBeforeCommitReceipt {
			t.Errorf("%q writes inside the repository and must be refused, got %q", cmd, reason)
		}
	}
	cmd := "git " + c + " -am x > " + filepath.Join(tmp, "commit.log")
	if reason, _ := PreflightDeny(cmd, sub); reason != "" {
		t.Errorf("%q writes outside the repository and must go to review, got %q", cmd, reason)
	}
}

// TestPreflightDenyRefusesAStepThatRunsBesideTheCommit: a pipeline member or a
// background job runs while git reads the tree, so it can change what lands after the
// reviewers read it.
func TestPreflightDenyRefusesAStepThatRunsBesideTheCommit(t *testing.T) {
	c := "co" + "mmit"
	for cmd, step := range map[string]string{
		"git " + c + " -am x | tee code.go":                    "tee code.go",
		"git " + c + " -am x |& tee code.go":                   "tee code.go",
		"git " + c + " -am x 2>&1 | sed -i s/a/b/ code.go":     "sed -i s/a/b/ code.go",
		"git add a | git " + c + " -m x":                       "git add a",
		"git " + c + " -am x | tail -3 > code.go":              "tail -3 > code.go",
		"git " + c + " -am x | { tail -3; } > code.go":         "{ tail -3; } > code.go",
		"(git " + c + " -am x | tail -3) | tee code.go":        "tee code.go",
		"git " + c + " -am x | tail -$(sed -i s/a/b/ code.go)": "sed -i s/a/b/ code.go",
		"git " + c + " -am x &":                                "git " + c + " -am x &",
		"git " + c + " -am x | tail -3 &":                      "git " + c + " -am x | tail -3 &",
		"git " + c + " -m x && echo done &":                    "git " + c + " -m x && echo done &",
		"{ git " + c + " -am x; } &":                           "{ git " + c + " -am x; } &",
		"git add a & git " + c + " -m x":                       "git add a &",
		"coproc git " + c + " -am x":                           "coproc git " + c + " -am x",
	} {
		reason, receipt := PreflightDeny(cmd, "/cwd")
		if receipt != StepBeforeCommitReceipt || reason != fmt.Sprintf(concurrentStepReason, step) {
			t.Errorf("%q runs %q beside git and must be refused naming it, got %q", cmd, step, reason)
		}
	}
	for _, cmd := range []string{
		"git " + c + " -am x 2>&1 | tail -3",
		"git " + c + " -m x | head -2",
		"git " + c + " -m x |& grep -v hint",
		"git " + c + " -m x | tail -3 > /tmp/commit.log",
		"git " + c + " -m x; sleep 5 &",
		"git status | head -2 && git " + c + " -m x",
	} {
		if reason, _ := PreflightDeny(cmd, "/cwd"); reason != "" {
			t.Errorf("%q runs nothing beside git that writes the tree and must go to review, got %q", cmd, reason)
		}
	}
}

// TestPreflightDenyRefusesACommitThatSetsGitsEnvironment: the gate's own git calls read
// the hook's environment, so an index, repository or work tree the command selects by
// variable is a tree the reviewers never see.
func TestPreflightDenyRefusesACommitThatSetsGitsEnvironment(t *testing.T) {
	c := "co" + "mmit"
	for _, cmd := range []string{
		"GIT_INDEX_FILE=/tmp/alt git " + c + " -m x",
		"env GIT_INDEX_FILE=/tmp/alt git " + c + " -m x",
		"env -u PAGER GIT_DIR=/other/.git git " + c + " -m x",
		`GIT_INDEX_FILE="$IDX" git ` + c + ` -m x`,
		"GIT_WORK_TREE=/other git add . && git " + c + " -m x",
		"export GIT_INDEX_FILE=/tmp/alt && git " + c + " -m x",
		"set -a; GIT_INDEX_FILE=/tmp/alt; git " + c + " -m x",
		"GIT_CONFIG_PARAMETERS=\"'core.worktree'='/other'\" git " + c + " -m x",
	} {
		if reason, receipt := PreflightDeny(cmd, "/cwd"); receipt != GitEnvironmentReceipt {
			t.Errorf("%q sets git's environment and must be refused, got %q", cmd, reason)
		}
	}
	for _, cmd := range []string{
		"GIT_AUTHOR_DATE=2026-01-01 GIT_COMMITTER_DATE=2026-01-01 git " + c + " -m x",
		"env LC_ALL=C GIT_EDITOR=true git " + c + " -m x",
		"git " + c + " -m x && GIT_DIR=/other/.git git log -1",
	} {
		if reason, _ := PreflightDeny(cmd, "/cwd"); reason != "" {
			t.Errorf("%q leaves the tree git commits unchanged and must go to review, got %q", cmd, reason)
		}
	}
}
