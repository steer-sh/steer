// Package checks owns a repository's check store: the manifest that declares each
// check Steer manages, the rules a declaration must satisfy, and the authoring
// guide. The store lives in the repository at .steer/checks, so a clone carries
// its declarations; it never carries authorization to run them.
package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// ManifestName is the manifest file inside the check store.
const ManifestName = "manifest.json"

// ManifestVersion is the manifest format this binary reads and writes.
const ManifestVersion = 1

// The binding kinds a declaration can name.
const (
	// KindScript is a repository script that Steer runs itself.
	KindScript = "script"
	// KindGoTest is one named test that stays in the repository's Go suite.
	KindGoTest = "go-test"
)

// ErrNoManifest reports a repository whose check store has no manifest.
var ErrNoManifest = errors.New("no check manifest")

// Manifest is the repository's list of managed checks.
type Manifest struct {
	Version int           `json:"version"`
	Checks  []Declaration `json:"checks"`
}

// Declaration names one check and binds it to what runs it.
//
// A script binding names Script. A go-test binding names Package, the
// repository-relative directory of the Go package, and Test, the exact top-level
// test function. Inputs lists further repository-relative files that belong to
// the detector, such as helpers and data files that analysis cannot find.
type Declaration struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Script  string   `json:"script,omitempty"`
	Package string   `json:"package,omitempty"`
	Test    string   `json:"test,omitempty"`
	Inputs  []string `json:"inputs,omitempty"`
}

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	goTestPattern = regexp.MustCompile(`^Test([^a-z].*)?$`)
	goIdent       = regexp.MustCompile(`^[\p{L}_][\p{L}\p{Nd}_]*$`)
)

// Dir returns the check store of the repository rooted at root.
func Dir(root string) string { return steerpath.ChecksDir(root) }

// ManifestPath returns the manifest of the repository rooted at root.
func ManifestPath(root string) string { return filepath.Join(Dir(root), ManifestName) }

// Validate reports the first rule the declaration breaks.
func (d Declaration) Validate() error {
	if !idPattern.MatchString(d.ID) {
		return fmt.Errorf("check id %q: use 1-64 letters, digits, - and _, starting with a letter or digit", d.ID)
	}
	switch d.Kind {
	case KindScript:
		if d.Script == "" {
			return fmt.Errorf("check %s: a %s binding names its script", d.ID, KindScript)
		}
		if d.Package != "" || d.Test != "" {
			return fmt.Errorf("check %s: a %s binding names no Go package or test", d.ID, KindScript)
		}
		if err := validRepoPath(d.Script); err != nil {
			return fmt.Errorf("check %s: script: %w", d.ID, err)
		}
	case KindGoTest:
		if d.Script != "" {
			return fmt.Errorf("check %s: a %s binding names no script", d.ID, KindGoTest)
		}
		if d.Package == "" || d.Test == "" {
			return fmt.Errorf("check %s: a %s binding names its package directory and test", d.ID, KindGoTest)
		}
		if d.Package != "." {
			if err := validRepoPath(d.Package); err != nil {
				return fmt.Errorf("check %s: package: %w", d.ID, err)
			}
		}
		if !goTestPattern.MatchString(d.Test) || !goIdent.MatchString(d.Test) {
			return fmt.Errorf("check %s: test %q is not a top-level Go test function name", d.ID, d.Test)
		}
	default:
		return fmt.Errorf("check %s: kind %q: expected %q or %q", d.ID, d.Kind, KindScript, KindGoTest)
	}
	for _, input := range d.Inputs {
		if err := validRepoPath(input); err != nil {
			return fmt.Errorf("check %s: input: %w", d.ID, err)
		}
	}
	return nil
}

// validRepoPath accepts a slash-separated path that stays inside the repository.
func validRepoPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("%q: use / as the separator", p)
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return fmt.Errorf("%q: use a repository-relative path", p)
	}
	if clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))); clean != p || !filepath.IsLocal(filepath.FromSlash(p)) {
		return fmt.Errorf("%q: use a clean path inside the repository", p)
	}
	return nil
}

// Validate reports the first rule the manifest breaks.
func (m Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("check manifest version %d: this steer reads version %d", m.Version, ManifestVersion)
	}
	seen := map[string]bool{}
	for _, d := range m.Checks {
		if err := d.Validate(); err != nil {
			return err
		}
		if seen[d.ID] {
			return fmt.Errorf("check id %q is declared twice", d.ID)
		}
		seen[d.ID] = true
	}
	return nil
}

// Load reads and validates the manifest of the repository rooted at root. A
// missing manifest returns ErrNoManifest.
func Load(root string) (Manifest, error) {
	data, err := os.ReadFile(ManifestPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, ErrNoManifest
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("read check manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", ManifestPath(root), err)
	}
	if m.Checks == nil {
		m.Checks = []Declaration{}
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestPath(root), err)
	}
	return m, nil
}

// Save validates the manifest and writes it with checks sorted by id. The write
// replaces the file in one rename, so a reader never sees a partial manifest. A
// manifest path that resolves outside root is refused: the repository carries any
// link on the way to it.
func Save(root string, m Manifest) error {
	if m.Checks == nil {
		m.Checks = []Declaration{}
	}
	if err := m.Validate(); err != nil {
		return err
	}
	sort.Slice(m.Checks, func(i, j int) bool { return m.Checks[i].ID < m.Checks[j].ID })
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := refuseOutsideManifest(root); err != nil {
		return err
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create check store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ManifestName+".*.tmp")
	if err != nil {
		return fmt.Errorf("write check manifest: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write check manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write check manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), ManifestPath(root)); err != nil {
		return fmt.Errorf("write check manifest: %w", err)
	}
	return nil
}

// refuseOutsideManifest refuses a manifest, or a directory holding it, that
// resolves outside root. The directory is tested apart because the temporary file
// and the rename land there wherever a link at the manifest points.
func refuseOutsideManifest(root string) error {
	for _, path := range []string{Dir(root), ManifestPath(root)} {
		if !steerpath.Within(root, path) {
			return fmt.Errorf("%s does not resolve inside %s; the check store writes only inside its repository", path, root)
		}
	}
	return nil
}

// Init creates an empty manifest in the repository rooted at root. An existing
// manifest is loaded and kept; created reports whether this call wrote one.
func Init(root string) (m Manifest, created bool, err error) {
	m, err = Load(root)
	if err == nil {
		return m, false, nil
	}
	if !errors.Is(err, ErrNoManifest) {
		return Manifest{}, false, err
	}
	m = Manifest{Version: ManifestVersion, Checks: []Declaration{}}
	if err := Save(root, m); err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}
