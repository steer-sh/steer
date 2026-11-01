package mine

import (
	"fmt"
	"os"
	"path/filepath"
)

// lockInventory holds a stable lock file across the complete read-modify-save.
// Keep the file after release: removing it permits two writers to lock different
// inodes. The OS releases the lock even when a writer exits before its defer runs.
func lockInventory(stateDir string) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, ".interventions.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockInventoryFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock intervention inventory: %w", err)
	}
	return func() {
		unlockInventoryFile(f)
		f.Close()
	}, nil
}
