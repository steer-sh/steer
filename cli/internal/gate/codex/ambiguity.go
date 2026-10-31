package codex

import "github.com/steer-sh/steer-cli/internal/gate/cc"

// ambiguousRepo reports that neither the payload nor the command text names the
// repository this commit lands in, so the gate cannot know which one it would review.
func ambiguousRepo(inv invocation) bool {
	return !inv.workdirGiven && !cc.RepoSelectedExplicitly(inv.command)
}

// ambiguousRepoReason is what Codex renders back to the model, so it names the retry.
const ambiguousRepoReason = "[commit-gate] BLOCKED: this commit names no repository, " +
	"and Codex did not report the directory the command runs in, so the gate cannot " +
	"tell which repository it would review. Re-run the commit with the repository " +
	"named by an ABSOLUTE path, either `git -C /absolute/path/to/repo commit ...` or " +
	"`cd /absolute/path/to/repo && git commit ...`. A relative path is not enough: it " +
	"resolves against the same directory the gate cannot see."

// ambiguousRepoReceipt is the operator's line, carried on the deny's systemMessage.
const ambiguousRepoReceipt = "[commit-gate] refused a Codex commit whose repository " +
	"the payload does not name. No review ran and no adjudication row was written."
