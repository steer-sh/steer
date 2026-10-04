package cc

import (
	"os"
	"path/filepath"
	"testing"
)

// Every case below is a bypass found on a real commit, or a reviewer finding on the fix
// for one, ported from hooks/commit-review/test_repo_dir.py with its provenance.
//
// The subcommand is spelled from a constant so running this file never trips the gate on
// itself.

const c = "co" + "mmit"

const testCWD = "/home/ubuntu/assistant_runtime"

// homeClaude cannot be a literal: `~` expands against the RUNNING user's home, so
// hardcoding the dev box's path failed these cases on every other machine while the
// parser was correct.
func homeClaude(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	return filepath.Join(home, ".claude")
}

func TestRepoDir(t *testing.T) {
	hc := homeClaude(t)
	cases := []struct {
		cmd, want, why string
	}{
		{"git " + c + " -m y", testCWD, "plain: the tool's cwd"},
		{"cd ~/.claude && git add x && git " + c + " -m y", hc, "BYPASS 2026-08-01: cd relocates the repo"},
		{"git -C ~/.claude " + c + " -m y", hc, "-C still honoured"},
		{"cd /tmp/r; git " + c + " -m y", "/tmp/r", "path token stops at ;"},
		{`cd "/tmp/my project" && git ` + c + " -m y", "/tmp/my project", "quoted path with space"},
		{"cd -P /tmp/r && git " + c + " -m y", "/tmp/r", "cd option flags skipped"},
		{"cd -- /tmp/r && git " + c + " -m y", "/tmp/r", "cd -- skipped"},
		{"git " + c + " -m y && cd /other", testCWD, "a cd AFTER the commit does not count"},
		{"git " + c + " -C HEAD", testCWD, "-C here is a commit ref, not a path"},
		{`git ` + c + ` -m "fix -C flag parsing"`, testCWD, "-C inside a message is prose"},
		{`git ` + c + ` -m "fix git -C /p handling"`, testCWD, "...even spelled `git -C`"},
		{"cd /tmp/r1 && cd /tmp/r2 && git " + c + " -m y", "/tmp/r2", "last cd wins, as the shell does"},
		{"cd /tmp/base && git -C sub " + c + " -m y", "/tmp/base/sub", "relative -C resolves off the cd"},
		{`git ` + c + ` -m "msg" && git -C /other status`, testCWD, "-C outside the commit invocation"},
		{"git -c user.email=x -C /tmp/r " + c + " -m y", "/tmp/r", "leading -c takes a non-dash value"},
		{"git config " + c + ".gpgsign true && cd /tmp/r && git " + c + " -m y", "/tmp/r",
			"`commit.gpgsign` is not a commit, and the match must not cross &&"},
		{`echo "running git ` + c + `" && cd /tmp/t && git ` + c + " -m y", "/tmp/t",
			"a literal in an earlier echo does not truncate the cd scan"},
		{"(cd /tmp/t && git " + c + " -m y)", "/tmp/t", "subshell grouping"},
		{"cd /tmp/outer && (cd inner && git " + c + " -m y)", "/tmp/outer/inner",
			"a nested scope inherits the outer shell's directory"},
		{"(cd /tmp/other) && git " + c + " -m y", testCWD,
			"a sibling subshell cannot relocate the committing shell"},
	}
	for _, tc := range cases {
		if got := RepoDir(tc.cmd, testCWD); got != tc.want {
			t.Errorf("RepoDir(%q) = %q, want %q — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

func TestDiffSources(t *testing.T) {
	cases := []struct {
		cmd              string
		notYetStaged     bool
		includeUntracked bool
		why              string
	}{
		{"git add engine/x.py && git " + c + " -q -F - <<EOF\nsubject\nEOF", true, true,
			"BYPASS 2026-08-01: PreToolUse fires before the add, so --cached is empty"},
		{"git add -A && git " + c + " -m 'x'", true, true, "the most common commit idiom"},
		{"git " + c + " -am 'x'", true, false,
			"-a stages TRACKED files only, so untracked paths are code that never lands"},
		{"git " + c + " -m 'x'", false, false, "pre-staged: --cached is correct"},
		{"git -C /r " + c + " -m 'x'", false, false, "-C, pre-staged"},
		{"git stage foo.py; git " + c + " -m 'x'", true, true, "stage alias"},
	}
	for _, tc := range cases {
		if got := DiffNotYetStaged(tc.cmd); got != tc.notYetStaged {
			t.Errorf("DiffNotYetStaged(%q) = %v, want %v — %s", tc.cmd, got, tc.notYetStaged, tc.why)
		}
		stagedOnly, untracked := DiffSources(tc.cmd)
		if stagedOnly != !tc.notYetStaged || untracked != tc.includeUntracked {
			t.Errorf("DiffSources(%q) = (%v,%v), want (%v,%v) — %s",
				tc.cmd, stagedOnly, untracked, !tc.notYetStaged, tc.includeUntracked, tc.why)
		}
	}
}

func TestIsCommitCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		why  string
	}{
		{"git add x && git " + c + " -m y", true, "compound"},
		{"git " + c + " -q -F - <<EOF", true, "heredoc message"},
		{"git status", false, "not a commit"},
		{"git " + c + " --dry-run", false, "rehearsal"},
		{"git " + c + " -m --dry-run", true, "the message value is not an option"},
		{"git " + c + " --message=--dry-run", true, "an attached message is not an option"},
		{"git config " + c + ".gpgsign true", false, "config key, not a commit"},
	}
	for _, tc := range cases {
		if got := IsCommitCommand(tc.cmd); got != tc.want {
			t.Errorf("IsCommitCommand(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

// A commit the parser cannot see because a wrapper hides it reaches the loose detector,
// and the gate refuses it before review rather than letting it run unreviewed.
func TestWrappedCommitIsRefused(t *testing.T) {
	for _, cmd := range []string{
		"bash -c 'git " + c + " -m x'",
		"eval \"git " + c + " -m x\"",
		"\\git " + c + " -m x",
		"sh -c \"cd /r && git " + c + " -m x\"",
	} {
		if !IsCommitCommand(cmd) {
			t.Errorf("IsCommitCommand(%q) = false, want true", cmd)
		}
		if reason, _ := PreflightDeny(cmd, "/r"); reason == "" {
			t.Errorf("PreflightDeny(%q) allowed a wrapped commit", cmd)
		}
	}
}

// A dry run is exempt only as the parsed call whose own options keep --dry-run in
// effect; the same text anywhere else exempts no commit.
func TestDryRunExemptsOnlyItsOwnCall(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		why  string
	}{
		{"bash -c 'git " + c + " -m \"fix --dry-run parsing\"'", true, "a wrapped message mentions the flag"},
		{"bash -c 'git " + c + " -m x' && git " + c + " --dry-run", true, "the dry run is a different call"},
		{"bash -c 'git " + c + " --dry-run'", true, "a wrapped dry run is not a parsed one"},
		{"git " + c + " --dry-run --no-dry-run -m x", true, "a later option clears the dry run"},
		{"git " + c + " --dry-run --no-dry -m x", true, "an abbreviation clears it too"},
		{"git " + c + " --dry-run \"$opt\"", true, "a dynamic word may clear it"},
		{"git " + c + " \"$opt\" --dry-run", true, "a dynamic word may take the flag as its value"},
		{"git " + c + " -m \"$msg\" --dry-run", false, "a dynamic message value is not an option"},
		{"git " + c + " --dry-run -- \"$path\"", false, "a dynamic pathspec is not an option"},
		{"/usr/bin/git " + c + " --dry-run", false, "a path to git"},
		{"echo $(git " + c + " --dry-run)", false, "a nested dry run"},
	}
	for _, tc := range cases {
		if got := IsCommitCommand(tc.cmd); got != tc.want {
			t.Errorf("IsCommitCommand(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
	cmd := "bash -c 'git " + c + " -m \"fix --dry-run parsing\"'"
	if reason, _ := PreflightDeny(cmd, "/r"); reason == "" {
		t.Errorf("PreflightDeny(%q) allowed a wrapped commit", cmd)
	}
}

// The deny stops the WHOLE command, so a file the command wrote is not on disk and a
// retry of the `git commit` half alone exits 128. The note says so, and only for the
// commands that can hit it.
func TestWritesAFile(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		why  string
	}{
		{"cat > m.txt <<'MSG'\nsubject\nMSG\ngit " + c + " -F m.txt", true,
			"the heredoc that writes the message file is exactly the case"},
		{"git " + c + " -m 'x' > out.log", true, "a plain redirect is destroyed by the deny the same way"},
		{"git " + c + " -m 'x'", false, "a command that writes nothing gets no note"},
		{"git " + c + " -m 'x' 2>&1", false, "`2>&1` writes no file"},
		{"git " + c + " -m 'rename a->b'", false, "an arrow in the message is prose, not a redirect"},
		{"git " + c + " -m 'x' >> out.log", true, "an append redirect writes a file"},
		{"git " + c + " -m 'x' 2>>log", true, "stderr append writes a file"},
	}
	for _, tc := range cases {
		if got := WritesAFile(tc.cmd); got != tc.want {
			t.Errorf("WritesAFile(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

// The reviewers read the message to rule on claim-vs-code drift. When it does not
// reach them they judge the diff alone, and that whole class goes silently unread.
// Every case here is a way the message arrives WRONG rather than missing, which the
// fallback string cannot warn about.
func TestExtractMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "onfile.txt"), []byte("message from the file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, cmd, want, why string
	}{
		{"written-heredoc",
			"D=/tmp/s\ncat > $D/msg.txt <<'MSG'\nsubject line\n\nbody line\nMSG\ngit add -A && git " + c + " -q -F $D/msg.txt",
			"subject line\n\nbody line",
			"the command writes the path it commits, so the heredoc IS the message"},
		{"stdin-heredoc", "git " + c + " -F - <<'MSG'\nfrom stdin\nMSG", "from stdin",
			"-F - keeps its own path to the heredoc body"},
		{"dash-m-wins", "cat > m.txt <<'MSG'\nheredoc\nMSG\ngit " + c + " -m 'inline'", "inline",
			"-m is the message when both forms appear"},
		{"file-on-disk", "git " + c + " -F onfile.txt", "message from the file\n",
			"a plain path with no heredoc is still read off disk"},
		{"unresolvable", "git " + c + " -F $D/msg.txt", messageNotFound,
			"a path the hook cannot resolve and no heredoc to fall back on"},
		{"foreign-heredoc", "python3 - <<'PY'\nprint(1)\nPY\ngit " + c + " -F $D/m.txt", messageNotFound,
			"a heredoc the commit does not write must never be passed off as the message"},
		{"two-heredocs",
			"python3 - <<'PY'\nprint(1)\nPY\ncat > m.txt <<'MSG'\nreal\nMSG\ngit " + c + " -F m.txt", "real",
			"the heredoc redirected into the -F path is the message"},
		{"tag-prefix-line",
			"cat > m.txt <<'MSG'\nsubject\n\nMSG_TIMEOUT unhandled\nMSG\ngit " + c + " -F m.txt",
			"subject\n\nMSG_TIMEOUT unhandled",
			"the closer is a whole line; a body line STARTING with the tag is body"},
		{"redirect-after-tag", "cat <<'MSG' > m.txt\nsubject\nMSG\ngit " + c + " -F m.txt", "subject",
			"the redirect may sit on either side of the heredoc operator"},
		{"double-quoted-tag", `cat > m.txt <<"MSG"` + "\nsubject\nMSG\ngit " + c + " -F m.txt", "subject",
			"a double-quoted tag is the same heredoc"},
		{"quoted-token", "cat > $D/m.txt <<'MSG'\nsubject\nMSG\ngit " + c + ` -F "$D/m.txt"`, "subject",
			"the -F token keeps its quotes; the redirect target does not"},
		{"foreign-heredoc-plus-write",
			"echo build > m.txt && python3 - <<'PY'\nprint(1)\nPY\ngit " + c + " -F m.txt", messageNotFound,
			"the write and the heredoc must be the SAME line"},
		{"stdin-foreign-heredoc",
			"python3 - <<'PY'\nprint(1)\nPY\ngit " + c + " -F - <<'MSG'\nsubject\nMSG", "subject",
			"-F - reads the heredoc attached to the COMMIT, not the first one in the command"},
		{"indented-closer",
			"cat > m.txt <<-'MSG'\n\tsubject\n\tMSG\ngit " + c + " -F m.txt", "\tsubject\n\t",
			"the shell grammar distinguishes an indented <<- closer from a prefix line"},
	}
	for _, tc := range cases {
		if got := ExtractMessage(tc.cmd, root); got != tc.want {
			t.Errorf("%s: ExtractMessage() = %q, want %q — %s", tc.name, got, tc.want, tc.why)
		}
	}
}

// A documentation heredoc is text. The body of a quoted heredoc that only writes a
// file leaves the span scan, so prose quoting `git commit` no longer fires the gate —
// but a body piped to an interpreter stays in the scan, or the cut is a bypass.
func TestDocumentHeredocBodiesAreNotCommands(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		why  string
	}{
		{"cat > doc.md <<'EOF'\nRun git " + c + " -m x\nEOF", false, "prose written to a file"},
		{"cat <<\"EOF\" > doc.md\ngit " + c + "\nEOF", false, "redirect before the tag"},
		{"tee doc.md <<'EOF'\ngit " + c + "\nEOF", false, "tee names the file"},
		{"cat > doc.md <<EOF\ngit " + c + "\nEOF", true, "unquoted tag expands, so the body is not inert"},
		{"bash <<'EOF' > log.txt\ngit " + c + " -m x\nEOF", true, "the interpreter runs the body"},
		{"python3 - <<'PY'\ngit " + c + "\nPY", true, "not a document writer"},
		{"cat > doc.md <<'EOF'\ngit " + c + "\nEOF\ngit " + c + " -m real", true, "the real invocation follows the body"},
		{"cat 1> doc.md <<'EOF'\ngit " + c + "\nEOF", false, "the redirect written with its descriptor"},
		{"cat << 'EOF' | bash\ngit " + c + " -m x\nEOF", true, "a space before the tag, then a pipe to a shell"},
		{"cat <<'EOF' | bash\ngit " + c + " -m x\nEOF", true, "piped to a shell, so the body runs"},
		{"tee log.txt <<'EOF' | bash\ngit " + c + " -m x\nEOF", true, "tee mirrors stdin to the pipe"},
		{"cat header.txt <<'EOF'\ngit " + c + " -m x\nEOF", true, "cat's own argument is a file it reads"},
		{"VAR=v tee <<'EOF'\ngit " + c + " -m x\nEOF", true, "the command word is not the file tee writes"},
		{"cat > doc.md <<'EOF'\ngit " + c + "\nEOF\n# done || true", false, "an or-list after the body is not a pipe"},
	}
	for _, tc := range cases {
		if got := IsCommitCommand(tc.cmd); got != tc.want {
			t.Errorf("IsCommitCommand(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

// A `cd` quoted inside a document body must not move the repo the gate reviews.
func TestDocumentHeredocBodiesDoNotMoveTheRepo(t *testing.T) {
	cmd := "cat > doc.md <<'EOF'\ncd /elsewhere\nEOF\ngit " + c + " -m x"
	if got := RepoDir(cmd, "/cwd"); got != "/cwd" {
		t.Errorf("RepoDir = %q, want /cwd", got)
	}
}

// The message reader keeps the whole body: masking serves the span scan alone.
func TestDocumentMaskingLeavesTheMessageIntact(t *testing.T) {
	cmd := "cat > m.txt <<'MSG'\nsubject line\nMSG\ngit " + c + " -F m.txt"
	if got := ExtractMessage(cmd, t.TempDir()); got != "subject line" {
		t.Errorf("ExtractMessage = %q", got)
	}
}

func TestRepoSelectedExplicitly(t *testing.T) {
	cases := map[string]bool{
		`git commit -m "x"`:                       false,
		`cd sub && git commit -m "x"`:             false,
		`cd ../other && git commit -m "x"`:        false,
		`git -C sub commit -m "x"`:                false,
		`git -C /r commit -m "x"`:                 true,
		`cd /r && git commit -m "x"`:              true,
		`(cd /r && git commit -m "x")`:            true,
		`cd '/r with space' && git commit -m "x"`: true,
		// The base an absolute cd set stays known, so a later relative hop is decisive.
		`cd /r && cd sub && git commit -m "x"`: true,
		`cd /r && git -C sub commit -m "x"`:    true,
		// A -C after the subcommand names a commit to reuse, not a directory.
		`git commit -C HEAD`: false,
		// The cd follows the commit, so it moves nothing the commit reads.
		`git commit -m "x" && cd /r`: false,
	}
	for cmd, want := range cases {
		if got := RepoSelectedExplicitly(cmd); got != want {
			t.Errorf("RepoSelectedExplicitly(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestRepoSelectedExplicitlyExpandsHome(t *testing.T) {
	if !RepoSelectedExplicitly(`cd ~/r && git commit -m "x"`) {
		t.Error("a home-relative path resolves without a base and must count")
	}
}

func TestRepoSelectedExplicitlyIncludesOuterScope(t *testing.T) {
	if !RepoSelectedExplicitly("cd /repo && (git " + c + " -m subject)") {
		t.Fatal("an absolute cd in an inherited scope selects the repository")
	}
}

func TestExactShellGrammarAdversarialCases(t *testing.T) {
	for _, cmd := range []string{
		"(git " + c + " -m x)",
		"echo $(git " + c + " -m x)",
		`git -c "x;y" ` + c + ` -m x`,
		"command git " + c + " -m x",
	} {
		if !IsCommitCommand(cmd) {
			t.Errorf("nested or wrapped commit was missed: %q", cmd)
		}
		if reason := AmbiguousShellReason(cmd, "/cwd"); reason != "" {
			t.Errorf("exact commit called ambiguous: %q: %s", cmd, reason)
		}
	}

	falseTrigger := `echo "release git ` + c + `"`
	if !IsCommitCommand(falseTrigger) {
		t.Fatal("the conservative detector must retain its false-positive posture")
	}
	if reason := AmbiguousShellReason(falseTrigger, "/cwd"); reason == "" {
		t.Fatal("loose-only evidence must be refused before repository inference")
	}
	if reason := AmbiguousShellReason("command echo git "+c, "/cwd"); reason == "" {
		t.Fatal("a wrapper's argument must remain loose-only evidence")
	}
	if reason := AmbiguousShellReason("git "+c+" -m one && git "+c+" -m two", "/cwd"); reason == "" {
		t.Fatal("one gate request cannot represent two commits")
	}

	if got := RepoDir(`cd "/tmp/a;b" && git -C sub `+c+` -m x`, "/cwd"); got != "/tmp/a;b/sub" {
		t.Errorf("quoted cd plus -C = %q", got)
	}
	if got := RepoDir(`git --git-dir=/meta --work-tree="/tmp/work tree" `+c+` -m x`, "/cwd"); got != "/tmp/work tree" {
		t.Errorf("explicit work tree = %q", got)
	}
	if reason := AmbiguousShellReason(`git --git-dir=/meta `+c+` -m x`, "/cwd"); reason == "" {
		t.Fatal("--git-dir alone does not establish the reviewed work tree")
	}

	if got := ExtractMessage(`echo -m 'wrong' && git `+c+` -m 'real'`, "/cwd"); got != "real" {
		t.Errorf("message = %q", got)
	}
	if got := ExtractMessage("git "+c+" -F - <<'MSG-1'\nsubject\nMSG-1", "/cwd"); got != "subject" {
		t.Errorf("punctuation heredoc message = %q", got)
	}
	if WritesAFile(`git ` + c + ` -m 'rename A->B'`) {
		t.Fatal("an arrow inside a message is not a redirect")
	}
}

func TestExtractMessageResolvesFileFromGitWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "message.txt"), []byte("subject\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := `git -C sub ` + c + ` -F message.txt`
	if reason := AmbiguousShellReason(cmd, root); reason != "" {
		t.Fatalf("literal message file was called ambiguous: %s", reason)
	}
	if got := ExtractMessage(cmd, root); got != "subject\n" {
		t.Errorf("message = %q", got)
	}
}

func TestExactCommitOptionsDoNotConfuseValuesWithFlags(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "absolute-message.txt")
	if err := os.WriteFile(abs, []byte("absolute\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cmd, message string
		all          bool
	}{
		{"git " + c + " -am 'subject'", "subject", true},
		{"git " + c + " -m '-a: subject'", "-a: subject", false},
		{"git " + c + " -- -a.txt", messageNotFound, false},
		{"git " + c + " -F- <<'MSG'\nstdin\nMSG", "stdin", false},
		{"git " + c + " --file=- <<'MSG'\nstdin\nMSG", "stdin", false},
		{"git " + c + " -F " + abs, "absolute\n", false},
	}
	for _, tc := range cases {
		if got := ExtractMessage(tc.cmd, root); got != tc.message {
			t.Errorf("ExtractMessage(%q) = %q, want %q", tc.cmd, got, tc.message)
		}
		if got := CommitsAllTracked(tc.cmd); got != tc.all {
			t.Errorf("CommitsAllTracked(%q) = %v, want %v", tc.cmd, got, tc.all)
		}
	}
}
