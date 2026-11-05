package cc

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/shellsyntax"
)

// PreflightDeny returns the refusal for a commit command the gate cannot review as
// written, and the receipt that says no review ran. Every harness adapter calls it
// before the engine, so a refusal consumes no firing and leaves a pending chain intact.
// Empty means the command goes to review.
func PreflightDeny(cmd, cwd string) (reason, receipt string) {
	if reason, receipt := DetectionDeny(cmd); reason != "" {
		return reason, receipt
	}
	if reason := AmbiguousShellReason(cmd, cwd); reason != "" {
		return reason, AmbiguousShellReceipt
	}
	if parsed, err := parseCommit(cmd, cwd); err == nil && parsed != nil {
		if option := parsed.gitLocationOption(); option != "" {
			return fmt.Sprintf(gitLocationReason, option), GitLocationReceipt
		}
		if names := parsed.gitEnvironment(); len(names) > 0 {
			return fmt.Sprintf(gitEnvironmentReason, strings.Join(names, ", ")), GitEnvironmentReceipt
		}
		if step := parsed.stepBeforeCommit(); step != "" {
			return fmt.Sprintf(stepBeforeCommitReason, shortStep(step)), StepBeforeCommitReceipt
		}
		if redir := parsed.commitRedirect(); redir != "" {
			return fmt.Sprintf(commitRedirectReason, shortStep(redir)), StepBeforeCommitReceipt
		}
		if step := parsed.concurrentStep(); step != "" {
			return fmt.Sprintf(concurrentStepReason, shortStep(step)), StepBeforeCommitReceipt
		}
		if parsed.selectsPaths() {
			return selectedPathsReason, SelectedPathsReceipt
		}
	}
	if reason := PartlyStagedReason(cmd, cwd); reason != "" {
		return reason, PartlyStagedReceipt
	}
	return "", ""
}

// DetectionDeny returns the refusal for a command whose commit text the parser cannot
// read as exactly the commit calls it found, and its receipt. It reads the text alone,
// so an adapter may call it before any check of the payload. Empty means Detect found
// no commit, or only parsed ones.
func DetectionDeny(cmd string) (reason, receipt string) {
	switch Detect(cmd) {
	case UnparsedCommit:
		_, err := shellsyntax.Parse(cmd)
		return fmt.Sprintf(ambiguousShellReason, fmt.Sprintf("the shell syntax could not be parsed (%v)", err)),
			AmbiguousShellReceipt
	case PossibleCommit:
		return possibleCommitReason, PossibleCommitReceipt
	case ParsedAndPossible:
		return possibleBesideCommitReason, PossibleCommitReceipt
	}
	return "", ""
}

const possibleCommitReason = "[commit-gate] BLOCKED: this command holds a possible " +
	"commit: `git ... commit` text that the gate cannot read as a plain command, such as " +
	"a commit inside bash -c or eval, the words inside a quoted argument, or a git " +
	"subcommand or command word that the shell expands from a variable or an escape. " +
	"NOTHING IN " +
	"THE COMMAND RAN. To search for the text, write a pattern that does not match " +
	"itself, for example `" + selfSafeSearchExample + "`. To commit, run git commit as " +
	"a plain command with the full intended message. This form keeps a multi-line " +
	"message whole:\n\n" + heredocCommitExample

// selfSafeSearchExample searches for commit text without holding it.
const selfSafeSearchExample = "rg 'git[ ]commit' cli/"

const possibleBesideCommitReason = "[commit-gate] BLOCKED: this command runs a commit " +
	"and also holds a possible commit outside it: `git ... commit` text that the gate " +
	"cannot read as a plain command, such as a second commit inside bash -c or eval. The " +
	"gate reviews only the commit it can read, so anything the other one commits would " +
	"land unreviewed. NOTHING IN THE COMMAND RAN. Run the commit as its own plain " +
	"command, and run the rest separately."

// PossibleCommitReceipt states that a possible commit was refused before review.
const PossibleCommitReceipt = "[commit-gate] refused a command that holds a possible commit " +
	"the parser could not read. No review ran and no adjudication row was written."

const gitLocationReason = "[commit-gate] BLOCKED: this command selects git's repository " +
	"or working tree with %s. The gate's own git calls do not apply that option, so " +
	"the reviewers could read a different repository, index or working tree. NOTHING " +
	"IN THE COMMAND RAN. Select the repository with `git -C /absolute/path/to/repo` " +
	"and run the commit without repository or working-tree overrides."

// GitLocationReceipt states that a repository override was refused before review.
const GitLocationReceipt = "[commit-gate] refused a commit with a repository or working-tree override. " +
	"No review ran and no adjudication row was written."

const gitEnvironmentReason = "[commit-gate] BLOCKED: this command sets %s for git. The " +
	"gate reads the repository, index and working tree from its own environment, so the " +
	"reviewers would read a tree other than the one this commit lands. NOTHING IN THE " +
	"COMMAND RAN. Make the default index hold exactly what this commit should carry, then " +
	"run the commit without that variable; select another repository with `git -C`. The " +
	"author, committer and editor variables stay permitted."

// GitEnvironmentReceipt states that the git-environment refusal blocked before review.
const GitEnvironmentReceipt = "[commit-gate] refused a commit that sets git's environment. " +
	"No review ran and no adjudication row was written."

const stepBeforeCommitReason = "[commit-gate] BLOCKED: this command runs `%s` before " +
	"its commit. The gate reads the tree before the command starts, so the reviewers " +
	"would not see what that step changes, and the commit would carry it unreviewed. " +
	"NOTHING IN THE COMMAND RAN. Run that step as its own command first, then the " +
	"commit. The commit's command may also hold cd, git add, and steps that change no " +
	"file, such as git status, git diff, cat or grep."

const commitRedirectReason = "[commit-gate] BLOCKED: this commit writes its output " +
	"with `%s`. The shell opens that file before git reads the tree, and `>` empties it, " +
	"so the commit can carry a change the reviewers never saw. NOTHING IN THE COMMAND " +
	"RAN. Drop the redirect, or send the output to /dev/null or to an absolute path " +
	"under /tmp outside the repository."

const concurrentStepReason = "[commit-gate] BLOCKED: this command runs `%s` while its " +
	"commit runs. A pipeline member or a background job runs beside git, so it can change " +
	"the tree after the reviewers read it and before git does. NOTHING IN THE COMMAND RAN. " +
	"Run the commit on its own, or pipe its output only to a filter such as tail, head or " +
	"grep that writes no file in the repository."

// StepBeforeCommitReceipt states that a refusal of a step before or beside the commit,
// or of a redirect on the commit itself, blocked before review.
const StepBeforeCommitReceipt = "[commit-gate] refused a commit whose command can change " +
	"the tree before or while git reads it. No review ran and no adjudication row was written."

// shortStep keeps a refused step's first line, cut to a length a reason can quote.
func shortStep(step string) string {
	line, _, cut := strings.Cut(step, "\n")
	if len(line) > 80 {
		line, cut = line[:80], true
	}
	if cut {
		line += " …"
	}
	return line
}

const selectedPathsReason = "[commit-gate] BLOCKED: this commit selects its own paths " +
	"(`git commit <paths>`, --only, --include or --pathspec-from-file). Those forms land a " +
	"tree the gate does not review: it reviews the index, or the working tree for a " +
	"bundled add. NOTHING IN THE COMMAND RAN, and the index is as it was. Make the index " +
	"hold exactly what this commit should carry, then run a plain commit with no paths."

// SelectedPathsReceipt states that the path-selecting commit refusal blocked before review.
const SelectedPathsReceipt = "[commit-gate] refused a commit that selects its own paths. " +
	"No review ran and no adjudication row was written."

// PartlyStagedReason refuses a bundled `git add ... && git commit` while a file holds
// both staged and unstaged edits. That form is reviewed from the working tree, so the
// reviewers would see the unstaged edits the commit does not carry. A plain commit
// reads the index and a `-a` commit stages every tracked edit, so neither is refused.
// A git that cannot answer refuses nothing.
func PartlyStagedReason(cmd, cwd string) string {
	if stagedOnly, _ := DiffSources(cmd); stagedOnly || CommitsAllTracked(cmd) {
		return ""
	}
	paths, ok := gate.PartlyStagedPaths(RepoDir(cmd, cwd))
	if !ok || len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf(partlyStagedReason, listPaths(paths, 10))
}

const partlyStagedReason = "[commit-gate] BLOCKED: this command stages files and commits " +
	"in one call, and some files are partly staged — the index holds some of their edits " +
	"and the working tree holds more: %s. The gate reviews a bundled add from the working " +
	"tree, so the reviewers would see edits this commit does not contain. NOTHING IN THE " +
	"COMMAND RAN, and the index is as it was. Run every step before the commit as its own " +
	"command, then run the commit alone: a plain commit is reviewed from the index. If a " +
	"file listed here should land whole, name it in that add."

// PartlyStagedReceipt states that the partly staged refusal blocked before review.
const PartlyStagedReceipt = "[commit-gate] refused a bundled add over partly staged files. " +
	"No review ran and no adjudication row was written."

func listPaths(paths []string, limit int) string {
	if len(paths) <= limit {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:limit], ", "), len(paths)-limit)
}
