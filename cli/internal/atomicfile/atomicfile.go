// Package atomicfile replaces a file through a temporary file in its directory,
// so a reader sees the old content or the new and never a partial write.
package atomicfile

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// WriteFile replaces path with data and sets its permissions to mode. On failure
// path is unchanged and the temporary file is removed.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	return Copy(path, bytes.NewReader(data), mode)
}

// Copy is WriteFile with the content streamed from r.
func Copy(path string, r io.Reader, mode os.FileMode) error {
	// A fixed prefix keeps the temporary name within the filesystem's name limit
	// whatever the length of path's own name.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".steer-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := fill(tmp, r, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// fill sets f's mode, copies r into it and closes it.
func fill(f *os.File, r io.Reader, mode os.FileMode) error {
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
