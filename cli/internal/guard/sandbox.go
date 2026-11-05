package guard

import (
	"fmt"
	"os"
	"path/filepath"
)

// AuditLogName is the file the runtime appends block rows to, beside the master.
const AuditLogName = "steer-audit.jsonl"

// PayloadsDirName is the directory the runtime writes evidence sidecars into,
// beside the master.
const PayloadsDirName = "steer-audit-payloads"

// Sandbox is a temporary store that holds byte-identical copies of store files.
// The runtime writes its audit rows and payload sidecars beside the master's own
// __file__, so a master run from a sandbox writes them into the sandbox and never
// into the live store the copy came from.
type Sandbox struct {
	// Dir is the store directory that copies land in.
	Dir  string
	root string
}

// NewSandbox creates an empty temporary store. Close removes it.
func NewSandbox(prefix string) (*Sandbox, error) {
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create guard sandbox: %w", err)
	}
	dir := filepath.Join(root, "store")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("failed to create guard sandbox store: %w", err)
	}
	return &Sandbox{Dir: dir, root: root}, nil
}

// Root is the sandbox's temporary directory, which holds Dir.
func (s *Sandbox) Root() string { return s.root }

// AuditPath is where a master run from this sandbox appends its audit rows.
func (s *Sandbox) AuditPath() string { return filepath.Join(s.Dir, AuditLogName) }

// Copy writes src's bytes to name, a slash-separated path relative to Dir, and
// returns the copy's path.
func (s *Sandbox) Copy(src, name string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", filepath.Base(src), err)
	}
	dst := filepath.Join(s.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("failed to create the directory for %s: %w", name, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", name, err)
	}
	return dst, nil
}

// Mkdir creates name, a slash-separated path relative to Dir.
func (s *Sandbox) Mkdir(name string) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, filepath.FromSlash(name)), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", name, err)
	}
	return nil
}

// Link creates name, a slash-separated path relative to Dir, as a symlink to
// target. It never reads target.
func (s *Sandbox) Link(target, name string) error {
	dst := filepath.Join(s.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("failed to create the directory for %s: %w", name, err)
	}
	if err := os.Symlink(target, dst); err != nil {
		return fmt.Errorf("failed to link %s: %w", name, err)
	}
	return nil
}

// Close removes the sandbox and everything written into it.
func (s *Sandbox) Close() error { return os.RemoveAll(s.root) }
