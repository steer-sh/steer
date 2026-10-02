// Package fixture stores a guard's test cases beside the guard and replays them.
// A stored case is rerunnable evidence; the counts register --self-test kept are
// not.
package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// Version is the stored file's format version.
const Version = 1

// DirName is the fixtures directory inside a guard store.
const DirName = "fixtures"

// Expected outcomes a case can declare.
const (
	ExpectBlock = "block"
	ExpectAllow = "allow"
)

// Origins a case can carry. A case mined from a corpus is a reconstruction:
// no corpus stores the hook payload a guard saw, so the payload is rebuilt from
// a diff or a transcript and never replayed from a record.
const (
	OriginSynthetic     = "synthetic"
	OriginReconstructed = "reconstructed"
)

// Source names the corpus row a reconstructed case came from. The finding
// fields name the reviewer finding the candidate was narrowed by, when the
// reader supplied one: the index identifies the block inside the reply, and the
// claim is what a person reads the case back by.
type Source struct {
	Corpus       string `json:"corpus,omitempty"`
	ID           string `json:"id,omitempty"`
	DiffFile     string `json:"diff_file,omitempty"`
	FindingIndex int    `json:"finding_index,omitempty"`
	FindingClaim string `json:"finding_claim,omitempty"`
}

// Case is one replayable test: the payload the guard sees, the filesystem
// context it reads, and the outcome the case asserts.
type Case struct {
	Name    string            `json:"name"`
	Payload map[string]any    `json:"payload"`
	Expect  string            `json:"expect"`
	Files   map[string]string `json:"files,omitempty"`
	Origin  string            `json:"origin"`
	Source  *Source           `json:"source,omitempty"`
}

// File is the stored document: one file per guard, in the guard store.
type File struct {
	Version int    `json:"version"`
	Hook    string `json:"hook"`
	Cases   []Case `json:"cases"`
}

// Path names a guard's fixture file inside a store directory.
func Path(storeDir, hook string) string {
	return filepath.Join(storeDir, DirName, hook+".json")
}

// Validate reports the first defect that would make a case unrunnable.
func (c Case) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("case name cannot be empty")
	}
	switch c.Expect {
	case ExpectBlock, ExpectAllow:
	default:
		return fmt.Errorf("case %q: expect must be %q or %q", c.Name, ExpectBlock, ExpectAllow)
	}
	switch c.Origin {
	case OriginSynthetic, OriginReconstructed:
	default:
		return fmt.Errorf("case %q: origin must be %q or %q", c.Name, OriginSynthetic, OriginReconstructed)
	}
	if len(c.Payload) == 0 {
		return fmt.Errorf("case %q: payload cannot be empty", c.Name)
	}
	for path := range c.Files {
		if filepath.IsAbs(path) || strings.Contains(path, "..") {
			return fmt.Errorf("case %q: file path %q must be relative and hold no ..", c.Name, path)
		}
	}
	return nil
}

// Validate checks the document and every case in it.
func (f File) Validate() error {
	if f.Version != Version {
		return fmt.Errorf("unsupported fixture version %d: expected %d", f.Version, Version)
	}
	if strings.TrimSpace(f.Hook) == "" {
		return fmt.Errorf("fixture file names no hook")
	}
	if len(f.Cases) == 0 {
		return fmt.Errorf("fixture file holds no cases")
	}
	names := map[string]struct{}{}
	for _, c := range f.Cases {
		if err := c.Validate(); err != nil {
			return err
		}
		if _, seen := names[c.Name]; seen {
			return fmt.Errorf("duplicate case name %q", c.Name)
		}
		names[c.Name] = struct{}{}
	}
	return nil
}

// Parse reads a fixture document and validates it.
func Parse(data []byte) (File, error) {
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("failed to parse fixtures: %w", err)
	}
	if err := file.Validate(); err != nil {
		return File{}, err
	}
	return file, nil
}

// Load reads a guard's stored fixtures. A missing file is not an error: a guard
// registered before fixtures existed simply has none.
func Load(storeDir, hook string) (File, bool, error) {
	data, err := os.ReadFile(Path(storeDir, hook))
	if os.IsNotExist(err) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("failed to read fixtures: %w", err)
	}
	file, err := Parse(data)
	if err != nil {
		return File{}, false, err
	}
	return file, true, nil
}

// Save writes a guard's fixtures into the store.
func Save(storeDir string, file File) error {
	if err := file.Validate(); err != nil {
		return err
	}
	path := Path(storeDir, file.Hook)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create fixtures directory: %w", err)
	}
	data, err := Encode(file)
	if err != nil {
		return err
	}
	return hookstore.WriteFile(path, data, 0o644)
}

// Remove drops a guard's fixtures. It is called with the guard.
func Remove(storeDir, hook string) error {
	if err := os.Remove(Path(storeDir, hook)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete fixtures: %w", err)
	}
	return nil
}

// Encode renders a fixture document, cases sorted by name, so a second write
// over identical cases is byte-identical and the digest is stable.
func Encode(file File) ([]byte, error) {
	sorted := make([]Case, len(file.Cases))
	copy(sorted, file.Cases)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	file.Cases = sorted
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode fixtures: %w", err)
	}
	return append(data, '\n'), nil
}

// Digest is sha256 over the encoded document, truncated to 16 hex characters —
// the same width mine.fileDigest uses for a guard.
func Digest(file File) string {
	data, err := Encode(file)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// FileDigest is sha256 over a file, truncated to 16 hex characters. An
// unreadable file yields no digest rather than a digest of nothing.
func FileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}
