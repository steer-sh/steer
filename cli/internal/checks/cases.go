package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CasesVersion is the case-file format this binary reads.
const CasesVersion = 1

// casesDirName holds one case file per check inside the check store.
const casesDirName = "cases"

// CaseFile is the replayable proof for one check: the trees it must pass and
// the trees it must fail.
type CaseFile struct {
	Version int    `json:"version"`
	Cases   []Case `json:"cases"`
}

// Case is one isolated filesystem tree and the outcome the check must produce
// on it. ExitCode 0 is a known-good case; a nonzero ExitCode is a defect case
// whose output must also contain Diagnostic. Setup is a Bash script run in the
// tree before the check, for state that plain files cannot express.
type Case struct {
	Name       string            `json:"name"`
	ExitCode   *int              `json:"exit_code"`
	Diagnostic string            `json:"diagnostic,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	Setup      string            `json:"setup,omitempty"`
	Provenance string            `json:"provenance"`
}

// CasesPath returns the case file of one check in the repository rooted at root.
func CasesPath(root, id string) string {
	return filepath.Join(Dir(root), casesDirName, id+".json")
}

// Validate reports the first rule the case file breaks.
func (f CaseFile) Validate() error {
	if f.Version != CasesVersion {
		return fmt.Errorf("case file version %d: this steer reads version %d", f.Version, CasesVersion)
	}
	var good, defect bool
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if !idPattern.MatchString(c.Name) {
			return fmt.Errorf("case name %q: use 1-64 letters, digits, - and _", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("case %q is declared twice", c.Name)
		}
		seen[c.Name] = true
		if c.ExitCode == nil {
			return fmt.Errorf("case %s: exit_code is required", c.Name)
		}
		switch code := *c.ExitCode; {
		case code == 0:
			good = true
		case code == 126 || code == 127:
			return fmt.Errorf("case %s: exit %d means a command could not run, so it cannot be an expected outcome", c.Name, code)
		case code < 0 || code > 255:
			return fmt.Errorf("case %s: exit_code %d is outside 0-255", c.Name, code)
		default:
			defect = true
			if c.Diagnostic == "" {
				return fmt.Errorf("case %s: a defect case names the diagnostic its failure must print", c.Name)
			}
		}
		if c.Provenance == "" {
			return fmt.Errorf("case %s: provenance is required", c.Name)
		}
		for p := range c.Files {
			if err := validRepoPath(p); err != nil {
				return fmt.Errorf("case %s: file: %w", c.Name, err)
			}
		}
	}
	if !good || !defect {
		return fmt.Errorf("a case file needs a known-good case (exit_code 0) and a defect case (nonzero exit_code)")
	}
	return nil
}

// LoadCases reads and validates one check's case file. It returns the file and
// the digest of its bytes, which a receipt binds.
func LoadCases(root, id string) (CaseFile, string, error) {
	path := CasesPath(root, id)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return CaseFile{}, "", fmt.Errorf("check %s has no cases: write %s", id, path)
	}
	if err != nil {
		return CaseFile{}, "", fmt.Errorf("read cases: %w", err)
	}
	var f CaseFile
	if err := json.Unmarshal(data, &f); err != nil {
		return CaseFile{}, "", fmt.Errorf("parse %s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return CaseFile{}, "", fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return f, hex.EncodeToString(sum[:]), nil
}
