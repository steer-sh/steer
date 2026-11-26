// Package jsonl appends rows to JSON Lines logs.
package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Append encodes v and appends it as one line to the file at path, creating the
// file with perm; noun names v in the encoding error.
func Append(path string, perm os.FileMode, noun string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to encode the %s: %w", noun, err)
	}
	name := filepath.Base(path)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", name, err)
	}
	defer file.Close()
	if _, err := file.Write(Separated(path, append(line, '\n'))); err != nil {
		return fmt.Errorf("failed to append to %s: %w", name, err)
	}
	return nil
}

// Load reads the JSON lines of the file at path, oldest first, skipping blank and
// unparsable lines and every value keep rejects. keep may normalize the value it
// is given. A missing file yields nil and no error.
func Load[T any](path string, keep func(*T) bool) ([]T, error) {
	name := filepath.Base(path)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", name, err)
	}
	defer file.Close()
	var out []T
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		if !keep(&v) {
			continue
		}
		out = append(out, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", name, err)
	}
	return out, nil
}

// Separated returns data led by a newline when the file at path ends mid-line, as
// a crash during an earlier append leaves it, so the next row starts a line of its
// own. A file that is absent, empty, unreadable or ends in a newline leaves data
// unchanged.
func Separated(path string, data []byte) []byte {
	f, err := os.Open(path)
	if err != nil {
		return data
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return data
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil || last[0] == '\n' {
		return data
	}
	return append([]byte{'\n'}, data...)
}
