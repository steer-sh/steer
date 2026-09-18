package checks

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// ErrNotValidated reports a validation run whose verdict was not validated.
var ErrNotValidated = errors.New("check did not validate")

// ErrNotDeclared is an id the manifest does not declare. A removal wraps it so the
// command layer can tell a miss from a removal that failed, and name the tiers that
// do declare the id.
var ErrNotDeclared = errors.New("no check is declared with this id")

func loadForMutation(root string) (Manifest, error) {
	m, err := Load(root)
	if errors.Is(err, ErrNoManifest) {
		return Manifest{}, fmt.Errorf("%w: run `steer check init` first", err)
	}
	return m, err
}

// Add validates a new check and declares it in the manifest only when every
// case matched. The run is recorded whatever its verdict; an unvalidated check
// returns the run with ErrNotValidated and leaves the manifest unchanged.
func Add(root string, d Declaration) (ValidationRun, error) {
	if err := d.Validate(); err != nil {
		return ValidationRun{}, err
	}
	m, err := loadForMutation(root)
	if err != nil {
		return ValidationRun{}, err
	}
	for _, existing := range m.Checks {
		if existing.ID == d.ID {
			return ValidationRun{}, fmt.Errorf("check %s is already declared; use `steer check test %s` to replay it", d.ID, d.ID)
		}
	}
	run, err := Validate(root, d)
	if err != nil {
		return run, err
	}
	if run.Verdict != VerdictValidated {
		return run, fmt.Errorf("%w: %s", ErrNotValidated, run.Verdict)
	}
	m.Checks = append(m.Checks, d)
	return run, Save(root, m)
}

// Test replays a declared check's cases with its current detector and records
// the run. A verdict other than validated returns ErrNotValidated.
func Test(root, id string) (ValidationRun, error) {
	m, err := loadForMutation(root)
	if err != nil {
		return ValidationRun{}, err
	}
	d, ok := find(m, id)
	if !ok {
		return ValidationRun{}, fmt.Errorf("no check %q is declared", id)
	}
	run, err := Validate(root, d)
	if err != nil {
		return run, err
	}
	if run.Verdict != VerdictValidated {
		return run, fmt.Errorf("%w: %s", ErrNotValidated, run.Verdict)
	}
	return run, nil
}

// Remove drops a check from the manifest and returns the archive entry that
// keeps its declaration with reason. Its case file and validation history stay,
// so evidence about the check outlives its declaration. A failed archive write
// stops the removal.
func Remove(root, id, reason string) (Declaration, string, error) {
	m, err := loadForMutation(root)
	if err != nil {
		return Declaration{}, "", err
	}
	d, ok := find(m, id)
	if !ok {
		return Declaration{}, "", fmt.Errorf("%w: %s", ErrNotDeclared, id)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return Declaration{}, "", err
	}
	archivePath, err := archive.Write(Dir(root), archive.Entry{
		Tier:     string(tier.Check),
		ID:       d.ID,
		Scope:    steerpath.ProjectScope,
		Reason:   reason,
		Manifest: raw,
	}, nil)
	if err != nil {
		return Declaration{}, "", fmt.Errorf("failed to archive check %s, so it was not removed: %w", d.ID, err)
	}
	kept := make([]Declaration, 0, len(m.Checks)-1)
	for _, c := range m.Checks {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	m.Checks = kept
	return d, archivePath, Save(root, m)
}

// Restore declares an archived check again through Add, so its cases replay
// first and a check that does not validate stays undeclared. The archive entry
// stays whatever the verdict.
func Restore(root string, s archive.Stored) (Declaration, ValidationRun, error) {
	if s.Tier != string(tier.Check) {
		return Declaration{}, ValidationRun{}, fmt.Errorf("archive entry %s holds a %s fix, not a check", s.Dir, s.Tier)
	}
	var d Declaration
	if err := json.Unmarshal(s.Manifest, &d); err != nil {
		return Declaration{}, ValidationRun{}, fmt.Errorf("archive entry %s: %w", s.Dir, err)
	}
	run, err := Add(root, d)
	return d, run, err
}

func find(m Manifest, id string) (Declaration, bool) {
	for _, d := range m.Checks {
		if d.ID == id {
			return d, true
		}
	}
	return Declaration{}, false
}
