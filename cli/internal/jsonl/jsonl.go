// Package jsonl appends rows to JSON Lines logs.
package jsonl

import "os"

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
