package status

import (
	"fmt"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// GuardTargets selects the candidates a guard, rule or registration repair writes
// to when the user names no harness, reading registrations when called. In the
// global scope these are the candidates holding a Steer registration, or every
// candidate while none holds one; the project scope keeps every candidate. It
// fails rather than leave out a candidate whose configuration cannot be read.
func GuardTargets(scope string, candidates []harness.Registrar) ([]harness.Registrar, error) {
	if scope != harness.GlobalScope {
		return candidates, nil
	}
	var held, left []harness.Registrar
	for _, c := range candidates {
		if holdsGlobalRegistration(c, candidates) {
			held = append(held, c)
		} else {
			left = append(left, c)
		}
	}
	for _, c := range left {
		if err := globalConfigReadable(c); err != nil {
			return nil, fmt.Errorf("%s's global configuration cannot be read, so Steer cannot tell "+
				"whether it is set up (%v); name the harnesses with --harness", HarnessLabel(string(c.Harness().ID)), err)
		}
	}
	if len(held) == 0 {
		return candidates, nil
	}
	return held, nil
}

// SetUpTargets narrows candidates to the harnesses holding a global Steer
// registration: the ones Steer is set up in. Unlike GuardTargets it never widens to
// every candidate, so a write Steer makes on its own reaches no harness the user did
// not set up.
func SetUpTargets(candidates []harness.Registrar) []harness.Registrar {
	var held []harness.Registrar
	for _, c := range candidates {
		if holdsGlobalRegistration(c, candidates) {
			held = append(held, c)
		}
	}
	return held
}

// globalConfigReadable fails when the harness's global instruction document or
// the file holding its global hook registrations exists but cannot be read.
func globalConfigReadable(r harness.Registrar) error {
	path, err := r.InstructionPath(harness.GlobalScope)
	if err != nil {
		return err
	}
	if _, err := instructions.SectionRules(path); err != nil {
		return err
	}
	switch r.Harness().ID {
	case harness.ClaudeCode:
		_, err := claude.LoadSettings(harness.GlobalScope)
		return err
	case harness.Codex:
		_, err := codex.HooksReadable(harness.GlobalScope, "")
		return err
	}
	_, err = r.MasterRegistered(harness.GlobalScope)
	return err
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
