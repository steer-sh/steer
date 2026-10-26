// Package apikey owns the API key the openai-compatible reviewer sends. The key
// never enters ~/.steer/config.json, which every user may read: it comes from the
// environment variable the config names, or from one file only its owner can read.
package apikey

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// fileName is the key file inside steerpath.CredentialsDir.
const fileName = "reviewer-api-key"

// SetCommand is the command that writes the key file. Every message that tells a
// person where the key goes names it, so no message sends a key into shell history.
const SetCommand = "steer reviewer key set"

// Where a resolved key came from.
const (
	SourceEnv     = "env"
	SourceFile    = "file"
	SourceMissing = "missing"
)

// Status is where a key would come from. It never carries the key.
type Status struct {
	// Source is SourceEnv, SourceFile or SourceMissing.
	Source string
	// Env is the variable name the configuration names, "" when it names none.
	Env string
	// Path is the key file.
	Path string
	// Problem is why the key file was refused, "" when it was not.
	Problem string
}

// Label is the status in a few words, as `steer reviewer key status` prints it.
func (s Status) Label() string {
	switch s.Source {
	case SourceEnv:
		return "env " + s.Env
	case SourceFile:
		return "file"
	}
	return "missing"
}

// Path returns the key file.
func Path() (string, error) {
	dir, err := steerpath.CredentialsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Find returns the key and its status. A non-empty variable that keyEnv names wins
// over the file. With neither, key is "" and the status says what was looked for.
func Find(keyEnv string) (string, Status) {
	keyEnv = strings.TrimSpace(keyEnv)
	st := Status{Source: SourceMissing, Env: keyEnv}
	if keyEnv != "" {
		if v := strings.TrimSpace(os.Getenv(keyEnv)); v != "" {
			st.Source = SourceEnv
			return v, st
		}
	}
	path, err := Path()
	if err != nil {
		st.Problem = err.Error()
		return "", st
	}
	st.Path = path
	key, err := readFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			st.Problem = err.Error()
		}
		return "", st
	}
	st.Source = SourceFile
	return key, st
}

// Missing words a status with no key for a ledger detail or an error: what was
// looked for, and the fix.
func (s Status) Missing() string {
	var looked []string
	if s.Env != "" {
		looked = append(looked, "$"+s.Env+" is unset or empty in this process")
	}
	switch {
	case s.Problem != "":
		looked = append(looked, s.Problem)
	case s.Path != "":
		looked = append(looked, "no key file at "+s.Path)
	}
	return "no reviewer API key: " + strings.Join(looked, "; ") + "; run " + SetCommand
}

// readFile reads the key file after it refuses a symlink, a file another user owns,
// and a file or directory other users may read.
func readFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("the key file %s is a symlink; remove it and run %s", path, SetCommand)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the key file %s is not a regular file; remove it and run %s", path, SetCommand)
	}
	if err := checkPrivate(path, info, 0o077); err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !dirInfo.IsDir() {
		return "", fmt.Errorf("%s is not a directory; remove it and run %s", dir, SetCommand)
	}
	if err := checkPrivate(dir, dirInfo, 0o077); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("the key file %s is empty; run %s", path, SetCommand)
	}
	return key, nil
}

// Write stores key in the key file: the directory at 0700, the file at 0600,
// through a temporary file renamed into place.
func Write(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("the key is empty; nothing was written")
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", errors.New("the key holds a line break; nothing was written")
	}
	path, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll, Chmod and CreateTemp all follow a symlink, which would put the key
	// wherever it points; readFile refuses that directory anyway.
	if info, err := os.Lstat(dir); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory (a symlink?); remove it and run %s again", dir, SetCommand)
	}
	// MkdirAll keeps the mode of a directory that already exists.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("restrict %s: %w", dir, err)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s exists and is not a regular file; remove it first", path)
	}
	tmp, err := os.CreateTemp(dir, "."+fileName+".*")
	if err != nil {
		return "", fmt.Errorf("stage the key file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("restrict the staged key file: %w", err)
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write the staged key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close the staged key file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace the key file: %w", err)
	}
	return path, nil
}

// Remove deletes the key file. removed is false when there was none.
func Remove() (path string, removed bool, err error) {
	path, err = Path()
	if err != nil {
		return "", false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return path, false, nil
		}
		return path, false, err
	}
	return path, true, nil
}
