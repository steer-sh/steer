package gate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// unstagedTag bounds the note naming paths whose working copy does not land.
const unstagedTag = "unstaged-paths"

// maxUnstagedPaths caps the paths the note names; the rest are counted.
const maxUnstagedPaths = 20

// UnstagedPaths returns the root-relative tracked paths whose working copy differs
// from the index. It asks the index git names in the environment, as the diff does,
// so a pre-commit run reads the temporary index git built for the commit.
// ok=false means git could not answer.
func UnstagedPaths(cwd string) ([]string, bool) {
	out, ok := Git(cwd, gitMedium, "diff", "--name-only", "--no-relative", "--no-renames", "-z")
	if !ok {
		return nil, false
	}
	return nulFields(out), true
}

// UnstagedNote renders the prompt section for a review of the index that a reviewer
// reading files from disk needs: the paths whose on-disk copy is not what lands.
// Paths the diff also changes come first, then the rest, each sorted; at most
// maxUnstagedPaths are named and the remainder counted. Empty when unstaged is.
func UnstagedNote(unstaged, diffPaths []string) string {
	return pathsNote(unstagedTag, "This commit lands the staged index. The working tree also holds edits "+
		"that this commit does NOT land, to the paths below, so a file you open on disk "+
		"at one of them is not the version under review. Do not raise a finding from "+
		"the on-disk copy of one of these paths.\n",
		" (also changed by this commit; only its staged part lands)",
		"- and %d more paths with unstaged edits\n", unstaged, diffPaths)
}

// pathsNote renders a tagged prompt section that names paths whose on-disk copy is
// not the version under review: intro, then the paths diffPaths also holds with
// inDiff after each, then the rest, at most maxUnstagedPaths in all, and rest
// counting the remainder. Empty when paths is.
func pathsNote(tag, intro, inDiff, rest string, paths, diffPaths []string) string {
	if len(paths) == 0 {
		return ""
	}
	inSet := map[string]bool{}
	for _, p := range diffPaths {
		inSet[p] = true
	}
	var both, other []string
	for _, p := range paths {
		if inSet[p] {
			both = append(both, p)
		} else {
			other = append(other, p)
		}
	}
	sort.Strings(both)
	sort.Strings(other)

	var b strings.Builder
	b.WriteString("\n<" + tag + ">\n")
	b.WriteString(intro)
	named := 0
	for _, group := range []struct {
		paths  []string
		suffix string
	}{{both, inDiff}, {other, ""}} {
		for _, p := range group.paths {
			if named == maxUnstagedPaths {
				break
			}
			b.WriteString("- " + promptPath(p) + group.suffix + "\n")
			named++
		}
	}
	if left := len(paths) - named; left > 0 {
		fmt.Fprintf(&b, rest, left)
	}
	b.WriteString("</" + tag + ">\n")
	return b.String()
}

// promptPath renders a repository path on one prompt line: boundary tags
// neutralised, and quoted when it holds a control character such as a newline.
func promptPath(p string) string {
	if strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		p = strconv.Quote(p)
	}
	return NeutralizeTags(p)
}
