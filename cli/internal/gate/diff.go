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
	excluded, specs := p.noisePathspecs(base)
	p.excluded = excluded

	// Pathspecs go LAST, so every option must be fixed before this point. `--stat`
	// appended after `--` is read as a pathspec, and git neither errors nor warns: it
	// returns the FULL diff under a header promising a stat, which is the unbounded
	// payload MaxDiffChars exists to prevent.
	p.diffArgs = append([]string{}, base...)
	p.statArgs = append(append([]string{}, base...), "--stat")
	p.nameArgs = append(append([]string{}, base...), "--name-only")
	if len(specs) > 0 {
		tail := append([]string{"--", ":/"}, specs...)
		p.diffArgs = append(p.diffArgs, tail...)
		p.statArgs = append(p.statArgs, tail...)
		p.nameArgs = append(p.nameArgs, tail...)
	}
	return p
}

// noisePathspecs returns the generated paths in this diff and the pathspecs that
// exclude their hunks.
//
// `top` magic because the repo may resolve to a SUBDIRECTORY, where a cwd-relative
// pathspec silently narrows the whole diff to that subtree. `literal` because a real
// path may contain glob metacharacters, and `:!:` treats its argument as a pattern.
func (p *diffPlan) noisePathspecs(base []string) (excluded, specs []string) {
	out, ok := Git(p.cwd, gitMedium, append(append([]string{}, base...), "--name-only")...)
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
	out, ok := Git(p.cwd, gitMedium, "ls-files", "--others", "--exclude-standard", "--full-name", "--", ":/")
	if !ok || out == "" {
		return nil
	}
	paths := dropCruft(nonEmptyLines(out))
	sort.Strings(paths)
	return paths
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
