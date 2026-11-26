package gate

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// The branch review reads one prompt over a branch's whole net change. Its digest is
// pinned beside the commit review's, and its rows carry BranchInstrumentSuffix, so
// no reader of commit-review rows mistakes one for the other.
var (
	//go:embed prompts/branch-review.md
	branchPrompt string
)

const (
	// MaxBranchDiffChars bounds the branch diff the prompt inlines. Above it whole
	// file-diffs are dropped smallest-first, as for a commit.
	MaxBranchDiffChars = 450_000
	// maxBranchCommitsChars bounds the commit messages the prompt inlines.
	maxBranchCommitsChars = 40_000
	// minBranchMessageChars is the least of each message a cut keeps.
	minBranchMessageChars = 300

	// BranchInstrumentSuffix marks rows of the branch review. Bump it when branch diff
	// assembly or finding parsing changes what the reviewers read or what counts.
	BranchInstrumentSuffix = "go-branch-v1"

	// dirtyTag bounds the note naming tracked edits that are not in HEAD.
	dirtyTag = "dirty-working-tree"
)

// branchBoundaryTags are the commit prompt's tags and the two only the branch
// prompt has. The commit review keeps its own list, so its inputs stay as they were.
var branchBoundaryTags = append(append([]string{}, promptBoundaryTags...), "commits", dirtyTag)

var branchBoundaryTagRe = regexp.MustCompile(
	`(?i)</?(?:` + strings.Join(branchBoundaryTags, "|") + `)(?:\s[^>]*)?>`)

// NeutralizeBranchTags is NeutralizeTags over the branch prompt's tags.
func NeutralizeBranchTags(text string) string {
	return branchBoundaryTagRe.ReplaceAllStringFunc(text, func(m string) string {
		return "&lt;" + m[1:]
	})
}

// BranchPromptDigest is the first 8 hex chars of sha256 of the branch prompt.
func BranchPromptDigest() string {
	sum := sha256.Sum256([]byte(branchPrompt))
	return hex.EncodeToString(sum[:])[:8]
}

// BranchInstrumentID is InstrumentIDWith for the branch review.
func (c Config) BranchInstrumentID(addendumDigest string) string {
	return c.instrumentID(BranchPromptDigest(), BranchInstrumentSuffix, addendumDigest)
}

// BranchInstrumentFields is InstrumentFieldsWith for the branch review.
func (c Config) BranchInstrumentFields(addendumDigest string) map[string]any {
	return c.instrumentFields(BranchPromptDigest(), BranchInstrumentSuffix, addendumDigest)
}

// BranchDiff is a branch's net change from its base commit to its head commit.
type BranchDiff struct {
	// Text is what the reviewers read; Raw is the unrendered diff.
	Text string
	Raw  string
	// Paths are the changed paths, root-relative, renames joined.
	Paths []string
	// RulePaths are the same paths with renames split, which reviewed rules match.
	RulePaths []string
}

// GetBranchDiff assembles the diff of base..head, both commit IDs, for a reviewer
// that can read the repository: noise is dropped and named as for a commit, and a
// diff over MaxBranchDiffChars drops whole files and tells the reviewer to read them.
// ok=false means git could not answer.
func GetBranchDiff(cfg Config, repo, base, head string) (BranchDiff, bool) {
	cfg.MaxDiffChars = MaxBranchDiffChars
	paths, ok := branchPaths(repo, base, head)
	if !ok {
		return BranchDiff{}, false
	}
	rulePaths, ok := branchPaths(repo, base, head, "--no-renames")
	if !ok {
		return BranchDiff{}, false
	}
	plan := planOver(cfg, repo, DiffSpec{}, []string{"diff", base, head})
	plan.branch = true
	diff, names, _, ok := plan.run()
	if !ok {
		return BranchDiff{}, false
	}
	return BranchDiff{Text: renderDiff(cfg, plan, diff, names, nil), Raw: diff,
		Paths: paths, RulePaths: rulePaths}, true
}

func branchPaths(repo, base, head string, extra ...string) ([]string, bool) {
	args := append([]string{"diff", "--name-only", "--no-relative", "-z"}, extra...)
	out, ok := Git(repo, gitMedium, append(args, base, head)...)
	if !ok {
		return nil, false
	}
	return dropCruft(nulFields(out)), true
}

// BranchCommits renders the messages of base..head, oldest first, each under a
// `commit <short id>` line, and counts the commits. Over maxBranchCommitsChars each
// message is cut to an equal share that keeps its subject; when a share would fall
// below minBranchMessageChars, only subjects are kept, the oldest cut first.
func BranchCommits(repo, base, head string) (text string, count int, ok bool) {
	out, ok := Git(repo, gitLong, "log", "--reverse", "--no-color", "--format=%x00commit %h%n%B",
		base+".."+head)
	if !ok {
		return "", 0, false
	}
	var messages []string
	for _, entry := range strings.Split(out, "\x00") {
		if entry = strings.TrimRight(entry, "\n"); entry != "" {
			messages = append(messages, entry)
		}
	}
	text = strings.Join(messages, "\n\n")
	if len(text) <= maxBranchCommitsChars || len(messages) == 0 {
		return text, len(messages), true
	}
	share := maxBranchCommitsChars / len(messages)
	if share < minBranchMessageChars {
		// Too many commits for a share of each body: subjects alone, then whole
		// entries from the oldest, until the rest fit.
		for i, message := range messages {
			messages[i] = message[:subjectEnd(message)]
		}
		kept, size := len(messages), -1
		for kept > 0 && size+len(messages[kept-1])+1 <= maxBranchCommitsChars {
			size += len(messages[kept-1]) + 1
			kept--
		}
		text = strings.Join(messages[kept:], "\n")
		if kept > 0 {
			text = fmt.Sprintf("[the %d oldest commits cut by the review harness; run git log to read them]\n", kept) + text
		}
		return text + "\n[message bodies cut by the review harness; run git log to read them]", len(messages), true
	}
	for i, message := range messages {
		if len(message) > share {
			cut := strings.LastIndexByte(message[:share], '\n')
			if cut <= 0 {
				cut = share
			}
			// The `commit <id>` line and the subject line stay whole.
			cut = max(cut, subjectEnd(message))
			messages[i] = message[:cut] + "\n[message cut by the review harness; run git log to read it whole]"
		}
	}
	return strings.Join(messages, "\n\n"), len(messages), true
}

// subjectEnd is the offset of the newline that ends a rendered message's subject
// line, the second line, or the message's length when it has none.
func subjectEnd(message string) int {
	first := strings.IndexByte(message, '\n')
	if first < 0 {
		return len(message)
	}
	second := strings.IndexByte(message[first+1:], '\n')
	if second < 0 {
		return len(message)
	}
	return first + 1 + second
}

// BuildBranchPrompt assembles the branch reviewer's prompt. It neutralises commits
// and diff itself; context is a rendered block. The addendum and the reviewed rules
// rulePaths match follow the context, as in the commit prompt.
func BuildBranchPrompt(base, head, commits, diff, context string, addendum Addendum, rulePaths []string) string {
	return fill(branchPrompt, map[string]string{
		"base_short": shortID(base), "head_short": shortID(head),
		"commits": NeutralizeBranchTags(commits), "diff": NeutralizeBranchTags(diff),
		"context": context + addendum.Block() + addendum.RulesBlock(rulePaths),
	})
}

func shortID(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// DirtyTreeNote renders the prompt section that names tracked paths whose working
// copy is not the HEAD the branch review reads. Empty when paths is.
func DirtyTreeNote(paths, branchPaths []string) string {
	// pathsNote neutralises the commit prompt's tags alone; these paths land in the
	// branch prompt, which has two more.
	named := make([]string, len(paths))
	for i, p := range paths {
		named[i] = NeutralizeBranchTags(p)
	}
	inBranch := make([]string, len(branchPaths))
	for i, p := range branchPaths {
		inBranch[i] = NeutralizeBranchTags(p)
	}
	return pathsNote(dirtyTag, "The branch diff ends at HEAD. The working tree also "+
		"holds edits that are not committed, to the paths below, so a file you open on "+
		"disk at one of them is not the version under review. Do not raise a finding "+
		"from the on-disk copy of one of these paths.\n",
		" (also changed by the branch; only its committed part is under review)",
		"- and %d more paths with uncommitted edits\n", named, inBranch)
}
