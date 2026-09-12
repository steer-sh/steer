package checks

import (
	"errors"
	"fmt"
)

// ErrNotValidated reports a validation run whose verdict was not validated.
var ErrNotValidated = errors.New("check did not validate")

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

// Remove drops a check from the manifest. Its case file and validation history
// stay, so evidence about the check outlives its declaration.
func Remove(root, id string) (Declaration, error) {
	m, err := loadForMutation(root)
	if err != nil {
		return Declaration{}, err
	}
	d, ok := find(m, id)
	if !ok {
		return Declaration{}, fmt.Errorf("no check %q is declared", id)
	}
	kept := make([]Declaration, 0, len(m.Checks)-1)
	for _, c := range m.Checks {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	m.Checks = kept
	return d, Save(root, m)
}

func find(m Manifest, id string) (Declaration, bool) {
	for _, d := range m.Checks {
		if d.ID == id {
			return d, true
		}
	}
	return Declaration{}, false
}
