package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Diff-pipeline cases ported from hooks/commit-review/test_inputs.py. Every mechanism
// here fails SILENTLY, which is why they are asserted rather than read.
//
// Real repositories, because the pathspec magic under test is git's own and a stub
// would assert a belief about it rather than its behaviour. The subcommand is spelled
// from a constant so a shell writing this file does not trip the gate on itself.
const verb = "co" + "mmit"

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

// buildRepo returns a real repo with everything staged.
func buildRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	gitCmd(t, root, "init", "-q", ".")
	for path, body := range files {
		writeFileAt(t, root, path, body)
	}
	gitCmd(t, root, "add", "-A")
	return root
}

func writeFileAt(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("line x\n")
	}
	return sb.String()
}

func stagedDiff(t *testing.T, cfg Config, dir string) string {
	t.Helper()
	diff, ok := GetDiff(cfg, dir, DiffSpec{StagedOnly: true})
	if !ok {
		t.Fatalf("GetDiff(%s) failed", dir)
	}
	return diff
}

func TestNoiseFilter(t *testing.T) {
	cfg := Defaults()
	code := lines(80)
	// `pkg[1]/` holds glob metacharacters, which must live in a path component around a
	// recognised name because the noise list is explicit basenames.
	root := buildRepo(t, map[string]string{
		"code.py":           code,
		"package-lock.json": strings.Repeat("lock\n", 200),
		"web/bun.lock":      strings.Repeat("l\n", 200),
		"pkg[1]/yarn.lock":  strings.Repeat("y\n", 200),
	})
	diff := stagedDiff(t, cfg, root)

	if !strings.Contains(diff, "code.py") {
		t.Error("the reviewable file must survive the filter")
	}
	if strings.Contains(diff, "+lock") {
		t.Error("lock hunks are what the filter exists to drop")
	}
	if !strings.Contains(diff, "package-lock.json") {
		t.Error("the PATH stays, or the message-vs-diff rule fires on a change the " +
			"reviewer was prevented from seeing")
	}
	if !strings.Contains(diff, "pkg[1]/yarn.lock") || strings.Contains(diff, "+y") {
		t.Error("`literal` magic: a path holding glob metacharacters must exclude " +
			"ITSELF, not whatever its brackets happen to match")
	}

	// A subdirectory cwd is how `cd sub` ahead of the subcommand resolves. A
	// cwd-relative pathspec would narrow the whole diff to that subtree, silently.
	sub := stagedDiff(t, cfg, filepath.Join(root, "web"))
	if !strings.Contains(sub, "code.py") {
		t.Error("`top` magic: filtering must not narrow the diff to the cwd's subtree")
	}
	if strings.Contains(sub, "+l\n") {
		t.Error("and it must still EXCLUDE from a subdirectory — the assertion above " +
			"only proves the inclusion was not narrowed, which is the weaker half")
	}

	// `diff.relative = true` makes --name-only cwd-relative, so every pathspec names
	// the wrong path. Git reports nothing for a pathspec matching nothing, so the
	// filter would fail open in silence.
	gitCmd(t, root, "config", "diff.relative", "true")
	rel := stagedDiff(t, cfg, filepath.Join(root, "web"))
	if strings.Contains(rel, "+l\n") || !strings.Contains(rel, "code.py") {
		t.Error("a repo setting diff.relative must not silently disarm the filter")
	}
	gitCmd(t, root, "config", "--unset", "diff.relative")
}

func TestOSCruftIsDroppedUnnamed(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{
		"code.py":       lines(80),
		".DS_Store":     strings.Repeat("\x00meta\n", 50),
		"web/.DS_Store": strings.Repeat("\x00meta\n", 50),
	})
	diff := stagedDiff(t, cfg, root)
	if !strings.Contains(diff, "code.py") {
		t.Error("the reviewable file must survive the filter")
	}
	if strings.Contains(diff, ".DS_Store") {
		t.Error("cruft is dropped WITHOUT being named — a named path is what the " +
			"reviewers keep raising as a finding")
	}

	untracked := buildRepo(t, map[string]string{"code.py": lines(80)})
	writeFileAt(t, untracked, ".DS_Store", "meta\n")
	writeFileAt(t, untracked, "new.py", lines(80))
	plan := &diffPlan{cfg: cfg, cwd: untracked}
	got := plan.untrackedPaths()
	if len(got) != 1 || got[0] != "new.py" {
		t.Errorf("untrackedPaths = %v, want [new.py] — untracked cruft is how it "+
			"reached the reviewers without ever being staged", got)
	}
}

// One file overrunning the budget alone leaves nothing to keep, so the diff degrades to
// the stat. A stat flag appended after `--` is read as a pathspec, and git neither
// errors nor warns — it returns the FULL diff under a header promising a stat.
func TestDegradedToStat(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{
		"big.py": strings.Repeat("y = 1\n", cfg.MaxDiffChars/4),
		"go.sum": strings.Repeat("sum\n", 200),
	})
	diff := stagedDiff(t, cfg, root)
	if !strings.Contains(diff, "too large to inline") || strings.Contains(diff, "@@") {
		t.Error("degraded mode must return a stat, not the diff it just refused to send")
	}
	if !strings.Contains(diff, "big.py") || len(diff) >= cfg.MaxDiffChars {
		t.Error("the stat must name the files and must itself be bounded")
	}
	if strings.Contains(diff, "read the working tree") || strings.Contains(diff, "READ ACCESS") {
		t.Error("the reviewer cannot run a command, so it must not be invited to read")
	}
}

// Many files, each individually affordable, together over budget: the drop tier. Sized
// off the budget so a budget change cannot silently make this a different case.
func TestDropTier(t *testing.T) {
	cfg := Defaults()
	small := strings.Repeat("z = 1\n", cfg.MaxDiffChars/120)
	files := map[string]string{"f00.py": strings.Repeat("y = 1\n", cfg.MaxDiffChars/8)}
	for i := 1; i < 7; i++ {
		files[fileName(i)] = small
	}
	root := buildRepo(t, files)
	diff := stagedDiff(t, cfg, root)

	// Asserted on the section header, not the bare name: a name is present either way,
	// via the dropped-file preamble or the stat, so a name check would pass on the
	// exact regression it exists to catch.
	if strings.Contains(diff, "diff --git a/f00.py") {
		t.Error("the one unaffordable file's hunks must be absent — this is what the " +
			"budget bought the others")
	}
	if !strings.Contains(diff, "f00.py") || !strings.Contains(diff, "dropped") {
		t.Error("and it must still be NAMED as dropped")
	}
	for i := 1; i < 7; i++ {
		if !strings.Contains(diff, "diff --git a/"+fileName(i)) {
			t.Errorf("%s must be kept — smallest-first must buy every affordable file", fileName(i))
		}
	}
	if len(diff) >= cfg.MaxDiffChars*13/10 {
		t.Error("the kept sections plus the stat and preamble must still be bounded")
	}
	if again := stagedDiff(t, cfg, root); again != diff {
		t.Error("an unchanged tree must produce a byte-identical diff, or the chain's " +
			"stop rule never fires and every round runs to the cap")
	}
	if strings.Count(diff, "diff --git ") != strings.Count(diff, "+++ b/") {
		t.Error("every kept section must be whole — a split section hands the reviewer " +
			"half a file under another file's header")
	}
}

func fileName(i int) string { return "f0" + string(rune('0'+i)) + ".py" }

func TestSmallDiffsAreNotPaddedPastTheSkip(t *testing.T) {
	cfg := Defaults()
	// The exclusion note is several hundred chars, so prepending it unconditionally
	// would carry an empty diff over the minimum and buy a review of no code at all.
	lockOnly := buildRepo(t, map[string]string{"package-lock.json": strings.Repeat("lock\n", 200)})
	if got := len(stagedDiff(t, cfg, lockOnly)); got >= cfg.MinDiffChars {
		t.Errorf("lock-only diff = %d chars, want < %d — it must skip, not review an "+
			"empty diff", got, cfg.MinDiffChars)
	}
	tiny := buildRepo(t, map[string]string{"a.py": "x = 1\n", "go.sum": strings.Repeat("sum\n", 200)})
	if got := len(stagedDiff(t, cfg, tiny)); got >= cfg.MinDiffChars {
		t.Errorf("tiny diff = %d chars, want < %d — the note must not inflate a "+
			"below-threshold diff past the skip", got, cfg.MinDiffChars)
	}
}

// Staging with `git add -A` beforehand sends the gate down the `git diff HEAD` path,
// and that command cannot see a file git has never tracked.
func TestUntrackedFiles(t *testing.T) {
	cfg := Defaults()
	code := lines(80)
	root := buildRepo(t, map[string]string{"old.py": code})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "old.py", code+"changed = 1\n")
	writeFileAt(t, root, "new.py", code)
	writeFileAt(t, root, "web/deep.py", code)
	writeFileAt(t, root, "yarn.lock", strings.Repeat("y\n", 200))
	writeFileAt(t, root, ".gitignore", "secret.txt\n")
	writeFileAt(t, root, "secret.txt", "nope\n")

	// Guards the case itself: without the untracked pass the new file is invisible, so
	// the assertions below would pass on any implementation.
	blind, _ := GetDiff(cfg, root, DiffSpec{})
	if strings.Contains(blind, "new.py") {
		t.Fatal("regression guard: a plain `git diff HEAD` must not see an untracked file")
	}

	spec := DiffSpec{IncludeUntracked: true}
	diff, ok := GetDiff(cfg, root, spec)
	if !ok {
		t.Fatal("GetDiff failed")
	}
	for _, name := range []string{"old.py", "new.py", "web/deep.py"} {
		if !strings.Contains(diff, name) {
			t.Errorf("%s: a file the change adds must reach the reviewer with its hunks", name)
		}
	}
	if !strings.Contains(diff, "new file mode") {
		t.Error("--no-index against /dev/null is what renders an untracked file as an " +
			"ordinary addition section the budget splitter can handle")
	}
	// Asserted on the CONTENT, not the name: `.gitignore` is itself untracked and its
	// own diff quotes the ignored path, so a name check passes on any implementation.
	if strings.Contains(diff, "+nope") {
		t.Error("--exclude-standard: an ignored file is not part of the change")
	}
	if strings.Contains(diff, "+y\n") {
		t.Error("an untracked lock file spends the budget on nothing reviewable")
	}
	if !strings.Contains(diff, "yarn.lock") {
		t.Error("its PATH still stays, or the message-vs-diff rule fires on it")
	}
	if strings.Count(diff, "diff --git ") != strings.Count(diff, "+++ b/") {
		t.Error("every section must be whole")
	}
	if again, _ := GetDiff(cfg, root, spec); again != diff {
		t.Error("an unchanged tree that reordered untracked files would fire to the cap " +
			"with nothing changing")
	}

	// A 0-byte file has the same CONTENT as /dev/null, so --no-index could reasonably
	// report no difference. Git special-cases /dev/null as nonexistent rather than
	// empty; this pins that behaviour rather than the belief about it.
	writeFileAt(t, root, "empty.py", "")
	empty, _ := GetDiff(cfg, root, spec)
	if !strings.Contains(empty, "b/empty.py") {
		t.Error("an empty file the change adds must be reported as added, never excluded")
	}
	if preamble := strings.SplitN(empty, "diff --git ", 2)[0]; strings.Contains(preamble, "empty.py") {
		t.Error("and it must not land in the exclusion preamble, which would tell the " +
			"reviewer its contents are generated")
	}
	if err := os.Remove(filepath.Join(root, "empty.py")); err != nil {
		t.Fatal(err)
	}

	paths, ok := ChangedFiles(cfg, root, spec)
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	want := []string{".gitignore", "new.py", "old.py", "web/deep.py", "yarn.lock"}
	if got := sortedUnique(paths); !equalStrings(got, want) {
		t.Errorf("ChangedFiles = %v, want %v — the path list drives the prose skip and "+
			"the cross-file check, so it must carry the same files the diff does", got, want)
	}
	fromSub, _ := ChangedFiles(cfg, filepath.Join(root, "web"), spec)
	if !equalStrings(sortedUnique(fromSub), want) {
		t.Errorf("from a subdirectory ChangedFiles = %v, want %v — `--full-name -- :/`: "+
			"bare ls-files --others lists that subtree ALONE", sortedUnique(fromSub), want)
	}
}

// Untracked paths must be appended to the name list in the same order as their
// sections. The budget splitter zips the two and fails CLOSED on a mismatch, so a
// desync degrades the diff to stat-only rather than announcing itself. Driven through a
// real over-budget diff, because that is the only path that calls the zip.
func TestUntrackedBudgetZip(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{"tracked.py": lines(80)})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "tracked.py", lines(80)+"changed = 1\n")
	writeFileAt(t, root, "one.py", strings.Repeat("a = 1\n", cfg.MaxDiffChars/12))
	writeFileAt(t, root, "two.py", strings.Repeat("b = 2\n", cfg.MaxDiffChars/12))
	diff, _ := GetDiff(cfg, root, DiffSpec{IncludeUntracked: true})
	if strings.Contains(diff, "too large to inline") {
		t.Error("a name list desynced from the sections fails closed to stat-only, " +
			"which is how this defect would return silently")
	}
	if !strings.Contains(diff, "dropped") || !strings.Contains(diff, "@@") {
		t.Error("the over-budget diff must still deliver real hunks for what fit")
	}
}

// TestUntrackedPathspecsNarrowTheScan pins the defect this fixed: a `git add <path>`
// stages one subtree, and an untracked file outside it can never land in the commit.
func TestUntrackedPathspecsNarrowTheScan(t *testing.T) {
	cfg := Defaults()
	code := lines(80)
	root := buildRepo(t, map[string]string{"old.py": code})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "cli/new.py", code)
	writeFileAt(t, root, "stray.md", code)

	wide, ok := GetDiff(cfg, root, DiffSpec{IncludeUntracked: true})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(wide, "stray.md") {
		t.Fatal("regression guard: an unnarrowed spec must still see every untracked file")
	}

	narrow, ok := GetDiff(cfg, root, DiffSpec{IncludeUntracked: true, UntrackedPathspecs: []string{"cli"}})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(narrow, "cli/new.py") {
		t.Error("a file the staging step names must reach the reviewer")
	}
	if strings.Contains(narrow, "stray.md") {
		t.Error("a file no `git add` names cannot land in this commit, so it is not the " +
			"reviewer's to spend a finding on")
	}
}

// TestTrackedPathspecsNarrowTheTrackedHalf: a `git add <path>` reads `git diff HEAD`,
// which shows every modified tracked file. A file that add does not name and the index
// does not hold stays in the working tree, so it is not part of this commit.
func TestTrackedPathspecsNarrowTheTrackedHalf(t *testing.T) {
	cfg := Defaults()
	code := lines(80)
	root := buildRepo(t, map[string]string{
		"cli/code.py": code, "PLAN.md": code, "earlier.py": code,
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "cli/code.py", code+"changed = 1\n")
	writeFileAt(t, root, "PLAN.md", code+"note = 1\n")
	writeFileAt(t, root, "earlier.py", code+"staged = 1\n")
	// Staged by an EARLIER command: this one's pathspec does not name it, and it lands
	// in the commit regardless.
	gitCmd(t, root, "add", "earlier.py")

	spec := DiffSpec{
		IncludeUntracked:   true,
		UntrackedPathspecs: []string{"cli/code.py"},
		TrackedPathspecs:   []string{"cli/code.py"},
	}
	diff, ok := GetDiff(cfg, root, spec)
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(diff, "cli/code.py") {
		t.Error("the path the staging step names must reach the reviewer")
	}
	if !strings.Contains(diff, "earlier.py") {
		t.Error("a file already in the index lands in this commit, so narrowing must " +
			"never drop it")
	}
	if strings.Contains(diff, "PLAN.md") {
		t.Error("an unstaged file no `git add` names cannot land in this commit, so it " +
			"is not the reviewer's to spend a finding on")
	}

	paths, ok := ChangedFiles(cfg, root, spec)
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	for _, path := range paths {
		if path == "PLAN.md" {
			t.Error("the file list is what the prose-only skip and the scope metadata " +
				"read, so it must narrow with the diff")
		}
	}
}

// TestUnreadablePathspecWidens: git rejects the pathspec, so the scan falls back to the
// whole repo rather than reporting a commit with no new files.
func TestUnreadablePathspecWidens(t *testing.T) {
	cfg := Defaults()
	code := lines(80)
	root := buildRepo(t, map[string]string{"old.py": code})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "new.py", code)

	bogus := []string{":(bogusmagic)x"}
	diff, ok := GetDiff(cfg, root, DiffSpec{IncludeUntracked: true, UntrackedPathspecs: bogus})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	if !strings.Contains(diff, "new.py") {
		t.Error("a pathspec git cannot read must widen to the repo, never drop the file")
	}

	// The tracked half fails harder: git rejects the whole vector, so an unprobed
	// pathspec would stop the diff and the review with it.
	writeFileAt(t, root, "old.py", code+"changed = 1\n")
	tracked, ok := GetDiff(cfg, root, DiffSpec{IncludeUntracked: true,
		UntrackedPathspecs: bogus, TrackedPathspecs: bogus})
	if !ok {
		t.Fatal("a pathspec git cannot read must widen the tracked half, never fail it")
	}
	if !strings.Contains(tracked, "old.py") {
		t.Error("and the tracked change must still reach the reviewer")
	}
}
