package hookstore

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LocalFileName reports whether name is a single path element: no separator, no
// directory traversal, and no volume or root prefix.
func LocalFileName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

// EntryFileName returns the guard file a manifest entry names, which is the
// entry's own file value or the key with a .py suffix. Both the key and the file
// value come from a file a repository can carry, and every reader joins the
// result to the store directory, so a name that is not a single path element is
// refused rather than repaired: a repaired name silently addresses a different
// file, and an unrepaired one addresses a file outside the store. store names the
// manifest the values were read from.
func EntryFileName(store, key, file string) (string, error) {
	trimmedKey := strings.TrimSpace(key)
	if !LocalFileName(trimmedKey) {
		return "", fmt.Errorf("guard manifest %s names an entry %q that is not a plain guard name", store, key)
	}
	name := strings.TrimSpace(file)
	if name == "" {
		name = trimmedKey + ".py"
	}
	if !LocalFileName(name) {
		return "", fmt.Errorf("guard manifest %s gives entry %q the file %q, which is not a plain file name; edit the manifest entry", store, trimmedKey, file)
	}
	return name, nil
}
