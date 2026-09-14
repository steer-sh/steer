package cc

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// PreflightDeny returns the refusal for a commit command the gate cannot review as
// written, and the receipt that says no review ran. Every harness adapter calls it
// before the engine, so a refusal consumes no firing and leaves a pending chain intact.
// Empty means the command goes to review.
func PreflightDeny(cmd, cwd string) (reason, receipt string) {
	if reason := AmbiguousShellReason(cmd, cwd); reason != "" {
		return reason, AmbiguousShellReceipt
	}
	if reason := PartlyStagedReason(cmd, cwd); reason != "" {
		return reason, PartlyStagedReceipt
	}
	return "", ""
}

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
