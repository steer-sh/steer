package cc

// CommitShapeGuidance tells an agent, before its first commit, the command form that
// PreflightDeny accepts. Each example it quotes passes PreflightDeny.
const CommitShapeGuidance = "[commit-gate] Steer reviews each `git commit` before it runs, " +
	"and it refuses, without running it, a commit command whose tree or message it cannot read. " +
	"Commit in this form:\n" +
	"- Run every other step first, each as its own command: tests, formatters, checks, " +
	"and `git add` with literal paths.\n" +
	"- Then run the commit alone, with its message on stdin:\n\n" + guidanceCommitExample + "\n\n" +
	"- Give the commit no file paths, --only or --include, and no output redirect; " +
	"put no $VAR or $(...) in its message.\n" +
	"- Commit text in a command's words is refused too: in bash -c, eval, a quoted " +
	"argument, or an interpreter heredoc such as `python3 - <<'EOF'`. Search for that " +
	"text with a pattern that does not match itself, for example `" + guidanceSearchExample +
	"`, and write it as data through a file tool or a quoted document heredoc such as `" +
	documentHeredocExample + "`."

// guidanceCommitExample is the commit form CommitShapeGuidance teaches.
const guidanceCommitExample = "git commit -F - <<'MSG'\nsubject\n\nbody\nMSG"

// guidanceSearchExample searches any repository for commit text without holding it.
const guidanceSearchExample = "rg 'git[ ]commit'"
