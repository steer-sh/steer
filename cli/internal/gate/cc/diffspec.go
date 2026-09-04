package cc

import "github.com/steer-sh/steer-cli/internal/gate"

// DiffSpecFor is the one owner of the tree the reviewers read for a Bash command.
// Every harness adapter calls it after the exact shell parse has passed.
func DiffSpecFor(cmd string) gate.DiffSpec {
	stagedOnly, includeUntracked := DiffSources(cmd)
	spec := gate.DiffSpec{StagedOnly: stagedOnly, IncludeUntracked: includeUntracked}
	if includeUntracked {
		spec.UntrackedPathspecs = StagedPathspecs(cmd)
	}
	return spec
}

// StagedPathspecs returns the literal paths named by structurally preceding adds.
// Nil means the command stages the whole repo or uses an unresolved path form.
func StagedPathspecs(cmd string) []string {
	if parsed, err := parseCommit(cmd, ""); err == nil && parsed != nil {
		return parsedStagedPathspecs(parsed.program, parsed)
	}
	return nil
}
