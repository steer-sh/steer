package status

import (
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// GuardTargets selects the candidates a guard, rule or registration repair writes
// to when the user names no harness, reading registrations when called. In the
// global scope these are the candidates holding a Steer registration, or every
// candidate while none holds one; the project scope keeps every candidate.
func GuardTargets(scope string, candidates []harness.Registrar) []harness.Registrar {
	if scope != harness.GlobalScope {
		return candidates
	}
	var held []harness.Registrar
	for _, c := range candidates {
		if holdsGlobalRegistration(c, candidates) {
			held = append(held, c)
		}
	}
	if len(held) == 0 {
		return candidates
	}
	return held
}

// holdsGlobalRegistration is globallyRegistered, plus a guard runner for a harness
// that reading does not cover, plus a global Steer Rules section carrying any line
// in a document no other candidate reads; uninstall keeps a shared one.
func holdsGlobalRegistration(r harness.Registrar, candidates []harness.Registrar) bool {
	g := harness.GlobalScope
	if globallyRegistered(r.Harness().ID) {
		return true
	}
	if registered, err := r.MasterRegistered(g); err == nil && registered {
		return true
	}
	path, err := r.InstructionPath(g)
	if err != nil || sharedDocument(r, path, candidates, nil) {
		return false
	}
	carried, err := instructions.SectionRules(path)
	return err == nil && len(carried) > 0
}

// sharedDocument reports whether another candidate, one for which only reports
// true when only is non-nil, reads the file at path as its global instruction
// document once symlinks resolve; a hard link is a separate document, as uninstall
// reads it.
func sharedDocument(r harness.Registrar, path string, candidates []harness.Registrar, only func(harness.ID) bool) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, c := range candidates {
		if c.Harness().ID == r.Harness().ID || (only != nil && !only(c.Harness().ID)) {
			continue
		}
		other, err := c.InstructionPath(harness.GlobalScope)
		if err != nil {
			continue
		}
		if o, err := filepath.EvalSymlinks(other); err == nil && o == real {
			return true
		}
	}
	return false
}
