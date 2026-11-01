package cadence

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ReadMarks maps a corpus to the size of its candidate pile as the last
// /steer-report pass left it. A pass that reads a one-off finding and makes no
// class of it leaves the pile in the queue, so the mark, not the pile, says
// whether a row holds anything the pass did not see.
type ReadMarks map[string]int

// ReadMarkPath holds the marks as JSON: corpus name to pile size.
func ReadMarkPath(dir string) string { return filepath.Join(dir, "read_marks.json") }

// LoadReadMarks reads the marks. A missing or unparsable file reads as no marks,
// so every row counts as unread on a machine where no pass recorded one.
func LoadReadMarks(dir string) ReadMarks {
	marks := ReadMarks{}
	data, err := os.ReadFile(ReadMarkPath(dir))
	if err != nil {
		return marks
	}
	if json.Unmarshal(data, &marks) != nil {
		return ReadMarks{}
	}
	return marks
}

// pileSize is a row's own count: chains for the review pile, clusters for the
// transcript pile. A new item, or one that returns to the pile, changes it.
func pileSize(c Candidate) int {
	return c.UndecidedChains + c.UngroupedClusters
}

// MarkRead records each named corpus's row in rows as read. A named corpus with
// no row has an empty pile, so its mark is dropped. It returns the marks written.
func MarkRead(dir string, rows []Candidate, corpora []string) (ReadMarks, error) {
	marks := LoadReadMarks(dir)
	for _, corpus := range corpora {
		delete(marks, corpus)
		for _, c := range rows {
			if c.Corpus == corpus {
				marks[corpus] = pileSize(c)
			}
		}
	}
	return marks, saveReadMarks(dir, marks)
}

// pruneReadMarks drops the mark of a corpus whose pile the queue no longer holds,
// so a later pile of the same size reads as new.
func pruneReadMarks(dir string, q *Queue) error {
	marks := LoadReadMarks(dir)
	changed := false
	for corpus := range marks {
		present := false
		for _, c := range q.Candidates {
			present = present || c.Corpus == corpus
		}
		if !present {
			delete(marks, corpus)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return saveReadMarks(dir, marks)
}

func saveReadMarks(dir string, marks ReadMarks) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marks, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(ReadMarkPath(dir), append(data, '\n'))
}

// Unread reports whether a candidate row differs from the pile the last pass read.
func Unread(c Candidate, marks ReadMarks) bool {
	size, ok := marks[c.Corpus]
	return !ok || size != pileSize(c)
}

// unreadCandidates counts the rows that differ from their marks.
func unreadCandidates(q *Queue, marks ReadMarks) int {
	n := 0
	for _, c := range q.Candidates {
		if Unread(c, marks) {
			n++
		}
	}
	return n
}
