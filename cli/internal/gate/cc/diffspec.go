package cc

import "github.com/steer-sh/steer-cli/internal/gate"

// DiffSpecFor is the one owner of the tree the reviewers read for a Bash command.
// Every harness adapter calls it after the exact shell parse has passed.
//
// The two pathspec lists hold the same paths in every form but one, and that form is
// why they are two. `git add x && git commit -am y` stages x AND every tracked
// modification: the add bounds which untracked files can land, while `-a` lands
// tracked files the add never named. Narrowing the tracked half there would hide a
// file the commit carries.
//
// cwd is the directory the shell starts in, as RepoDir takes it.
func DiffSpecFor(cmd, cwd string) gate.DiffSpec {
	stagedOnly, includeUntracked := DiffSources(cmd)
	spec := gate.DiffSpec{StagedOnly: stagedOnly, IncludeUntracked: includeUntracked,
		Amend: Amends(cmd)}
	if includeUntracked {
		spec.UntrackedPathspecs = StagedPathspecs(cmd, cwd)
	}
	if !stagedOnly && !CommitsAllTracked(cmd) {
		spec.TrackedPathspecs = StagedPathspecs(cmd, cwd)
	}
	return spec
}

// StagedPathspecs returns the literal paths named by structurally preceding adds,
// each call's directory resolved from cwd. Nil means the command stages the whole
// repo or uses an unresolved path form.
func StagedPathspecs(cmd, cwd string) []string {
	if parsed, err := parseCommit(cmd, cwd); err == nil && parsed != nil {
		return parsedStagedPathspecs(parsed.program, parsed)
	}
	return nil
}
