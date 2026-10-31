package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// poisonedProject writes a project store whose manifest names one entry, with no
// guard file for it, and returns the store directory and the manifest bytes.
func poisonedProject(t *testing.T, key, file string) (string, []byte) {
	t.Helper()
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	manifestPath, err := GetManifestFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"hooks":{"` + key + `":{"file":"` + file + `","rule":"r","source":"manual"}}}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []byte(raw)
}

// TestARemovalRefusesAManifestNameOutsideTheStore covers a repository that ships
// its own project manifest: the name it carries reaches a delete, so a removal
// that joined it would archive and delete a file the store does not own.
func TestARemovalRefusesAManifestNameOutsideTheStore(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts RemoveOptions
	}{
		{"archived removal", RemoveOptions{Reason: "poisoned"}},
		// CatalogCopy skips the archive, so the delete is the only step left.
		{"catalog removal", RemoveOptions{CatalogCopy: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, manifestRaw := poisonedProject(t, "x", "../../victim")
			victim := filepath.Join(filepath.Dir(filepath.Dir(dir)), "victim")
			if err := os.WriteFile(victim, []byte("SECRET"), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := RemoveManagedHook(ProjectScope, "x", tc.opts)
			if err == nil {
				t.Fatal("the removal accepted a file name that leaves the store")
			}
			if !strings.Contains(err.Error(), `"x"`) || !strings.Contains(err.Error(), "../../victim") {
				t.Fatalf("the error does not name the entry or its file: %v", err)
			}

			content, readErr := os.ReadFile(victim)
			if readErr != nil || string(content) != "SECRET" {
				t.Fatalf("the file outside the store = %q, %v", content, readErr)
			}
			now, err := os.ReadFile(filepath.Join(dir, "steer-manifest.json"))
			if err != nil || string(now) != string(manifestRaw) {
				t.Fatalf("the refused removal rewrote the manifest: %q, %v", now, err)
			}
			if entries, err := os.ReadDir(filepath.Join(dir, "archive")); err == nil && len(entries) > 0 {
				t.Fatalf("the refused removal archived %d entries", len(entries))
			}
		})
	}
}

// TestAManifestKeyOutsideTheStoreIsRefused covers the second route to a path: an
// entry with no file value is read as <key>.py, so validating the file value
// alone would leave the key unchecked.
func TestAManifestKeyOutsideTheStoreIsRefused(t *testing.T) {
	poisonedProject(t, "../../victim", "")
	if _, err := LoadManifest(ProjectScope); err == nil {
		t.Fatal("the manifest reader accepted a key that leaves the store")
	} else if !strings.Contains(err.Error(), "../../victim") {
		t.Fatalf("the error does not name the entry: %v", err)
	}
}

// TestArchivingAGuardReadsOnlyInsideTheStore covers the step that copies a file
// into the repository. A plain file name passes every name check and can still
// be a link to a file the store does not own.
func TestArchivingAGuardReadsOnlyInsideTheStore(t *testing.T) {
	dir := fixtureProject(t, map[string]string{"alpha": blockingHook("ALPHA")})
	secret := filepath.Join(filepath.Dir(filepath.Dir(dir)), "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alpha.py")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("this filesystem does not take symlinks: %v", err)
	}

	if _, err := RemoveManagedHook(ProjectScope, "alpha", RemoveOptions{Reason: "poisoned"}); err == nil {
		t.Fatal("the removal archived a link that leaves the store")
	}
	if content, err := os.ReadFile(secret); err != nil || string(content) != "SECRET" {
		t.Fatalf("the file outside the store = %q, %v", content, err)
	}
	found := false
	_ = filepath.Walk(filepath.Join(dir, "archive"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if body, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(body), "SECRET") {
			found = true
		}
		return nil
	})
	if found {
		t.Fatal("the archive holds the contents of a file outside the store")
	}
}
