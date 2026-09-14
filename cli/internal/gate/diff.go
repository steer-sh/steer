package gate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DiffSpec says which tree the diff is a delta against. The adapters answer it from
// different evidence — the Claude Code adapter simulates the shell command, the git
// adapter reads the index — but the engine below sees only this.
type DiffSpec struct {
	// StagedOnly selects `git diff --cached` over `git diff HEAD`.
	StagedOnly bool
	// IncludeUntracked adds files git has never seen, rendered as additions.
	IncludeUntracked bool
	// UntrackedPathspecs narrows those files to what the command's own staging step
	// names. Empty means every untracked file in the repo.
	UntrackedPathspecs []string
	// TrackedPathspecs narrows the tracked diff to the same paths, joined with the
	// index. Empty means every tracked change. The two lists differ for a command
	// that stages a path AND commits with -a: the add bounds what new files can land,
	// while -a lands tracked files it never named.
	TrackedPathspecs []string
}

// Equal compares two specs by value. The pathspec list makes DiffSpec uncomparable
// with ==, and a round that silently stopped comparing them would report every retry
// as spec-unchanged.
func (s DiffSpec) Equal(other DiffSpec) bool {
	if s.StagedOnly != other.StagedOnly || s.IncludeUntracked != other.IncludeUntracked {
		return false
	}
	return equalPathspecs(s.UntrackedPathspecs, other.UntrackedPathspecs) &&
		equalPathspecs(s.TrackedPathspecs, other.TrackedPathspecs)
}

func equalPathspecs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffPlan owns the three argument vectors that must agree — the diff, --name-only
// and --stat — and the untracked text appended to two of them. budgetSections zips
// the diff's sections onto the name list and fails closed on a length mismatch, so a
// future edit that touches one vector and not the other degrades every affected
// commit to stat-only, silently. One owner is what makes that undrifting.
type diffPlan struct {
	cfg  Config
	cwd  string
	spec DiffSpec

	diffArgs []string
	nameArgs []string
	statArgs []string

	// included narrows every vector to the files this commit lands. Empty means the
	// whole tree.
	included []string

	// excluded names what the harness dropped and TELLS the reviewer. OS cruft is
	// dropped from the same pathspecs and deliberately absent here.
	excluded []string
}

// newDiffPlan builds the argument vectors and the exclusion pathspecs.
func newDiffPlan(cfg Config, cwd string, spec DiffSpec) *diffPlan {
	base := []string{"diff"}
	if spec.StagedOnly {
		base = append(base, "--cached")
	} else {
		base = append(base, "HEAD")
	}
	// --no-relative on every call, and it is not about the pathspecs. It is already
	// the default, but a repo setting `diff.relative = true` restricts git diff to the
	// cwd's SUBTREE, so a commit made from a subdirectory would be reviewed with every
	// file outside it missing — silently, since the diff still parses and simply
	// describes less than the commit. It also pins --name-only root-relative, which is
	// what the pathspecs' `top` resolves against.
	base = append(base, "--no-relative")
	// --function-context expands every hunk to its whole enclosing function. Without
	// it git ships three lines either side, so a five-line edit inside a forty-line
	// function reaches the reviewer as a fragment — and a reviewer that cannot see the
	// rest of the function infers it, which is where hallucinated findings come from.
	base = append(base, "--function-context")

	p := &diffPlan{cfg: cfg, cwd: cwd, spec: spec}
	p.included = trackedSelector(cwd, spec)
	excluded, specs := p.noisePathspecs(base)
	p.excluded = excluded

	// Pathspecs go LAST, so every option must be fixed before this point. `--stat`
	// appended after `--` is read as a pathspec, and git neither errors nor warns: it
	// returns the FULL diff under a header promising a stat, which is the unbounded
	// payload MaxDiffChars exists to prevent.
	p.diffArgs = append([]string{}, base...)
	p.statArgs = append(append([]string{}, base...), "--stat")
	p.nameArgs = append(append([]string{}, base...), "--name-only")
	if tail := pathspecTail(p.included, specs); len(tail) > 0 {
		p.diffArgs = append(p.diffArgs, tail...)
		p.statArgs = append(p.statArgs, tail...)
		p.nameArgs = append(p.nameArgs, tail...)
	}
	return p
}

// pathspecTail assembles the `--` argument tail from the inclusions and the
// exclusions. `:/` stands in for the inclusions when there are none, because an
// exclusion needs a positive pathspec beside it to subtract from; with inclusions
// present, `:/` would widen the diff back to the whole tree.
func pathspecTail(included, excluded []string) []string {
	if len(included) == 0 && len(excluded) == 0 {
		return nil
	}
	tail := []string{"--"}
	if len(included) > 0 {
		tail = append(tail, included...)
	} else {
		tail = append(tail, ":/")
	}
	return append(tail, excluded...)
}

// trackedSelector returns the pathspecs that narrow the tracked diff to the files
// this commit lands: the files the command's own specs select, plus what the index
// already holds, each as a `top,literal` path. Nil means no narrowing.
//
// git resolves the command's specs to files first, so an exclusion among them cannot
// drop an index path. A spec git rejects widens, like the untracked scan.
func trackedSelector(cwd string, spec DiffSpec) []string {
	if spec.StagedOnly || len(spec.TrackedPathspecs) == 0 {
		return nil
	}
	named, ok := Git(cwd, gitMedium, append([]string{"diff", "HEAD", "--name-only",
		"--no-relative", "--no-renames", "-z", "--"}, spec.TrackedPathspecs...)...)
	if !ok {
		return nil
	}
	staged, ok := Git(cwd, gitMedium, "diff", "--cached", "--name-only", "--no-relative",
		"--no-renames", "-z")
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var specs []string
	for _, path := range append(strings.Split(named, "\x00"), strings.Split(staged, "\x00")...) {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		specs = append(specs, ":(top,literal)"+path)
	}
	// Nil would widen an empty selection to the whole tree; the command's own specs
	// keep it empty.
	if len(specs) == 0 {
		return append([]string{}, spec.TrackedPathspecs...)
	}
	return specs
}

// noisePathspecs returns the generated paths in this diff and the pathspecs that
// exclude their hunks.
//
// `top` magic because the repo may resolve to a SUBDIRECTORY, where a cwd-relative
// pathspec silently narrows the whole diff to that subtree. `literal` because a real
// path may contain glob metacharacters, and `:!:` treats its argument as a pattern.
func (p *diffPlan) noisePathspecs(base []string) (excluded, specs []string) {
	args := append(append([]string{}, base...), "--name-only")
	args = append(args, pathspecTail(p.included, nil)...)
	out, ok := Git(p.cwd, gitMedium, args...)
	if !ok || out == "" {
		return nil, nil
	}
	var dropped []string
	for _, path := range nonEmptyLines(out) {
		if IsNoise(path) {
			excluded = append(excluded, path)
			dropped = append(dropped, path)
			continue
		}
		// Cruft is excluded from the diff but absent from `excluded`, so the preamble
		// never mentions it.
		if IsOSCruft(path) {
			dropped = append(dropped, path)
		}
	}
	for _, path := range dropped {
		specs = append(specs, ":(exclude,top,literal)"+path)
	}
	return excluded, specs
}

// run executes the plan and returns the diff text, the matching name list, and the
// paths of any untracked files rendered into both. The untracked append happens here
// so the two streams cannot drift apart.
func (p *diffPlan) run() (diff, names string, newPaths []string, ok bool) {
	diff, ok = Git(p.cwd, gitLong, p.diffArgs...)
	if !ok {
		return "", "", nil, false
	}
	names, _ = Git(p.cwd, gitLong, p.nameArgs...)

	if !p.spec.IncludeUntracked {
		return diff, names, nil, true
	}
	candidates := p.untrackedPaths()
	var reviewable []string
	for _, path := range candidates {
		if IsNoise(path) {
			p.excluded = append(p.excluded, path)
			continue
		}
		reviewable = append(reviewable, path)
	}
	text, used, skipped := p.untrackedSections(reviewable)
	p.excluded = append(p.excluded, skipped...)
	diff += text
	for _, path := range used {
		names += path + "\n"
	}
	return diff, names, used, true
}

func (p *diffPlan) stat(newPaths []string) string {
	stat, _ := Git(p.cwd, gitLong, p.statArgs...)
	// --stat is blind to untracked files for the same reason the diff was. Naming
	// them keeps the "Full stat" claim true.
	if len(newPaths) > 0 {
		stat += "\n new files: " + strings.Join(newPaths, ", ")
	}
	return stat
}

// untrackedPaths lists root-relative paths git has never seen, honouring .gitignore.
//
// `--full-name -- :/` for the reason the plan passes --no-relative: from a
// subdirectory, bare `ls-files --others` lists that subtree ALONE, so a commit made
// from `sub/` would report every new file outside sub as nonexistent.
func (p *diffPlan) untrackedPaths() []string {
	base := []string{"ls-files", "--others", "--exclude-standard", "--full-name", "--"}
	out, ok := Git(p.cwd, gitMedium, append(append([]string{}, base...), p.untrackedSelector()...)...)
	if !ok {
		// A pathspec git rejects leaves the intent unresolved, so widen rather than
		// drop: a review missing a new file the commit lands is the worse direction.
		out, ok = Git(p.cwd, gitMedium, append(append([]string{}, base...), ":/")...)
	}
	if !ok || out == "" {
		return nil
	}
	paths := dropCruft(nonEmptyLines(out))
	sort.Strings(paths)
	return paths
}

// untrackedSelector is the pathspec list that limits the untracked scan. `:/` is the
// whole repo, which is what an unnarrowed spec means.
func (p *diffPlan) untrackedSelector() []string {
	if len(p.spec.UntrackedPathspecs) == 0 {
		return []string{":/"}
	}
	return p.spec.UntrackedPathspecs
}

// untrackedSections renders `paths` as additions and reports which it covered and which
// it skipped.
//
// `--no-index` against /dev/null is what makes an untracked file render as an ordinary
// `new file mode` section. The only budget here is per-file: budgetSections spends the
// aggregate one and names what it dropped.
func (p *diffPlan) untrackedSections(paths []string) (text string, used, skipped []string) {
	if len(paths) == 0 {
		return "", nil, nil
	}
	top := Toplevel(p.cwd)
	var sb strings.Builder
	for _, path := range paths {
		if size, ok := fileSize(filepath.Join(top, path)); !ok || size > int64(p.cfg.MaxDiffChars) {
			skipped = append(skipped, path)
			continue
		}
		// Git() cannot run this: --no-index exits 1 whenever the two sides differ,
		// which is every call here, and Git() reads any non-zero as failure.
		out, code, err := runGitAllowingExit1(top, "diff", "--no-index", "--no-color", "--", os.DevNull, path)
		// budgetSections splits on a column-0 `diff --git ` and fails closed without
		// one, so a header-less result is skipped rather than appended.
		if err != nil || (code != 0 && code != 1) || !strings.HasPrefix(out, "diff --git ") {
			skipped = append(skipped, path)
			continue
		}
		sb.WriteString(out)
		used = append(used, path)
	}
	return sb.String(), used, skipped
}

func runGitAllowingExit1(cwd string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return out.String(), exitErr.ExitCode(), nil
		}
		return "", -1, err
	}
	return out.String(), 0, nil
}

// budgetSections returns the whole file-diffs that fit maxChars and the paths dropped to
// make them fit. ok=false means the budget buys nothing — one file overrunning it alone,
// or a diff this cannot parse — and the caller falls back to the stat.
//
// Whole sections only, chosen smallest-first, ordered deterministically on (size, path).
// Sections are emitted in the diff's order, not size order.
func budgetSections(diff, names string, maxChars int) (kept string, dropped []string, ok bool) {
	if !strings.HasPrefix(diff, "diff --git ") {
		return "", nil, false
	}
	// A `diff --git ` at column 0 cannot occur inside hunk content: every line of a
	// hunk body carries a leading ' ', '+', '-' or '\'. So a bare header at column 0
	// always starts a file, including when the diff's own text is a patch file.
	var sections []string
	var cur strings.Builder
	for _, line := range splitKeepEnds(diff) {
		if strings.HasPrefix(line, "diff --git ") && cur.Len() > 0 {
			sections = append(sections, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		sections = append(sections, cur.String())
	}
	// Paths come from --name-only rather than from parsing the `diff --git a/x b/x`
	// header, whose two-path form is ambiguous for any path containing a space and is
	// quoted for any path containing a non-ASCII byte. Both commands walk the same
	// diff queue under the same pathspecs, so they agree in order; a length mismatch
	// means that assumption broke and is a fail-closed, not something to reconcile.
	paths := nonEmptyLines(names)
	if len(paths) != len(sections) {
		return "", nil, false
	}

	order := make([]int, len(sections))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if len(sections[ia]) != len(sections[ib]) {
			return len(sections[ia]) < len(sections[ib])
		}
		return paths[ia] < paths[ib]
	})

	keep := map[int]bool{}
	spent := 0
	for _, i := range order {
		// Ascending, so the first section that does not fit ends the scan: every one
		// after it is at least as large against an unchanged budget.
		if spent+len(sections[i]) > maxChars {
			break
		}
		keep[i] = true
		spent += len(sections[i])
	}
	if len(keep) == 0 {
		return "", nil, false
	}
	// Sections are emitted in the DIFF's order, not size order — the ranking chooses
	// the set, never the presentation.
	var out strings.Builder
	for i := range sections {
		if keep[i] {
			out.WriteString(sections[i])
		} else {
			dropped = append(dropped, paths[i])
		}
	}
	return out.String(), dropped, true
}

func splitKeepEnds(text string) []string {
	var out []string
	for len(text) > 0 {
		idx := strings.IndexByte(text, '\n')
		if idx < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:idx+1])
		text = text[idx+1:]
	}
	return out
}

// ChangedFiles lists the paths this commit touches, root-relative.
//
// --no-relative for the same reason as the diff plan, plus one of its own: `git grep`
// in referencingFiles reports root-relative paths, so a cwd-relative list here would
// fail every "is this file in the diff" comparison and attach changed files as
// unchanged callers. ok=false means git could not answer — fail open.
func ChangedFiles(cfg Config, cwd string, spec DiffSpec) (paths []string, ok bool) {
	args := []string{"diff"}
	if spec.StagedOnly {
		args = append(args, "--cached")
	} else {
		args = append(args, "HEAD")
	}
	args = append(args, "--name-only", "--no-relative")
	args = append(args, pathspecTail(trackedSelector(cwd, spec), nil)...)
	out, ok := Git(cwd, gitMedium, args...)
	if !ok {
		return nil, false
	}
	paths = dropCruft(nonEmptyLines(out))
	if spec.IncludeUntracked {
		plan := &diffPlan{cfg: cfg, cwd: cwd, spec: spec}
		paths = append(paths, plan.untrackedPaths()...)
	}
	return paths, true
}

// DiffResult is the rendered diff plus the raw one it came from. The pair is the
// point: rendering SHORTENS an over-budget diff, so Text answers "what do the
// reviewers read" while only Raw answers "is there enough here to review" and "did
// this commit change since the last round". A stat-only render is byte-identical
// across two different versions of an over-budget file, so it cannot carry identity.
type DiffResult struct {
	Text string
	Raw  string
}

// GetDiff assembles the diff text the reviewers read: filtered, budgeted, and
// preambled with what the harness dropped. An empty return means git could not
// answer.
func GetDiff(cfg Config, cwd string, spec DiffSpec) (string, bool) {
	result, ok := GetDiffSized(cfg, cwd, spec)
	return result.Text, ok
}

// GetDiffSized is GetDiff carrying the raw length as well.
func GetDiffSized(cfg Config, cwd string, spec DiffSpec) (DiffResult, bool) {
	plan := newDiffPlan(cfg, cwd, spec)
	diff, names, newPaths, ok := plan.run()
	if !ok {
		return DiffResult{}, false
	}
	return DiffResult{Text: renderDiff(cfg, plan, diff, names, newPaths), Raw: diff}, true
}

func renderDiff(cfg Config, plan *diffPlan, diff, names string, newPaths []string) string {
	if len(diff) > cfg.MaxDiffChars {
		stat := plan.stat(newPaths)
		kept, dropped, ok := budgetSections(diff, names, cfg.MaxDiffChars)
		if !ok {
			// Nothing fits: one file's own diff overruns the whole budget. Stat only.
			// This says what is missing and invites no reads — the reviewer cannot run
			// a command, so telling it to read the working tree is a false claim that
			// costs latency on tool calls that can never resolve.
			diff = fmt.Sprintf("[diff is %d chars — too large to inline, and no single "+
				"file's diff fits the budget. Stat below is all that is available; "+
				"the hunks are NOT visible to you. Do not raise a finding about "+
				"code you cannot see here.]\n\n%s", len(diff), stat)
		} else {
			diff = fmt.Sprintf("[The review harness dropped %d file(s) from the diff "+
				"below to fit its budget, NOT the commit: %s. These files ARE part of "+
				"the commit and their hunks are not visible to you. Do not raise a "+
				"finding about them, and do not treat a change the message describes "+
				"as missing because its file is one of them. Full stat:]\n\n%s\n\n%s",
				len(dropped), strings.Join(dropped, ", "), stat, kept)
		}
	} else if len(diff) < cfg.MinDiffChars {
		// Return it bare. The preamble below is several hundred chars, so prepending
		// it to a tiny diff would carry it over MinDiffChars and buy a review of
		// nothing — and a commit touching ONLY lock files would review an empty diff.
		return diff
	}
	if len(plan.excluded) > 0 {
		// Say what was dropped, or the message-vs-diff rule fires on it: "bump deps"
		// against a diff with no lock file in it is a finding the reviewer is right to
		// raise and wrong about, and that false positive is manufactured by this filter.
		diff = "[Excluded from the diff below by the review harness, NOT by the " +
			"commit: " + strings.Join(plan.excluded, ", ") + ". These files ARE part of the " +
			"commit; their contents are generated and were dropped to spend the " +
			"budget on reviewable code. Do not raise a finding because a change " +
			"the message describes is not visible in one of them.]\n\n" + diff
	}
	return diff
}

// SlashPaths returns the paths with OS separators normalised to forward slashes, the
// form the ledger stores so a scope's path regex reads one separator on every host.
// git already emits forward slashes, so this only ever changes a Windows-authored path.
func SlashPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.ReplaceAll(p, "\\", "/")
	}
	return out
}

// HasUnstagedEdits reports whether the working tree holds edits to tracked files that
// the index does not have. Untracked files are deliberately outside it: a scratch file
// nobody staged is not evidence that a fix went missing, and counting it would fire the
// stale-index notice on every repo with a stray file in it.
//
// ok=false from git reads as "no unstaged edits" — fail open, like every other read here.
func HasUnstagedEdits(cfg Config, cwd string) bool {
	out, ok := Git(cwd, gitMedium, "diff", "--name-only", "--no-relative")
	return ok && strings.TrimSpace(out) != ""
}

// PartlyStagedPaths returns the root-relative paths that differ in both halves: the
// index holds a change to them and the working tree holds a further one. A command
// that stages other paths and commits lands their index content, while a working-tree
// diff shows both. ok=false means git could not answer, never an empty set.
func PartlyStagedPaths(cwd string) ([]string, bool) {
	staged, ok := Git(cwd, gitMedium, "diff", "--cached", "--name-only", "--no-relative", "--no-renames", "-z")
	if !ok {
		return nil, false
	}
	unstaged, ok := Git(cwd, gitMedium, "diff", "--name-only", "--no-relative", "--no-renames", "-z")
	if !ok {
		return nil, false
	}
	inIndex := map[string]bool{}
	for _, path := range strings.Split(staged, "\x00") {
		if path != "" {
			inIndex[path] = true
		}
	}
	var out []string
	for _, path := range strings.Split(unstaged, "\x00") {
		if path != "" && inIndex[path] {
			out = append(out, path)
		}
	}
	return out, true
}

// StaleIndexNotice is agent-facing text for a retry that reads the index while edits
// sit unstaged in the working tree. It names only what was observed: the round before
// it may have read either tree.
func StaleIndexNotice() string {
	return "[commit-gate] THIS COMMAND REVIEWS THE INDEX, and you have unstaged edits " +
		"in the working tree. The gate reads the tree from the command's shape, and a " +
		"denied command never ran its own staging step, so what is staged may predate " +
		"your fixes and the reviews above may be of code you already replaced. Stage " +
		"your edits and re-issue the command whole if that is not what you meant.\n\n"
}
