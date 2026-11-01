package adjudication

import (
	"os"
	"path/filepath"
	"testing"
)

// Each stream that ends in half a line, as a crash mid-append leaves it, must
// still read the next appended entry.
func TestAppendAfterATornLineIsRead(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		append func(dir string) error
		count  func(dir string) (int, error)
	}{
		{"receipts", LogName,
			func(dir string) error {
				return Append(dir, Receipt{Subject: SubjectGuard, FiringID: "aa", Judgment: Prevented})
			},
			func(dir string) (int, error) { r, err := Load(dir); return len(r), err }},
		{"contests", ContestLogName,
			func(dir string) error {
				return AppendContest(dir, Contest{Subject: SubjectCheck, RunID: "61f2fc58fd179f86", Reason: "flaky"})
			},
			func(dir string) (int, error) { r, err := LoadContests(dir); return len(r), err }},
		{"exclusions", ExclusionLogName,
			func(dir string) error {
				return AppendExclusion(dir, Exclusion{Subject: SubjectGuard, RowDigest: "d", Action: Reinstate, Reason: "real work"})
			},
			func(dir string) (int, error) { r, err := LoadExclusions(dir); return len(r), err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(`{"subject":"gu`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := tc.append(dir); err != nil {
				t.Fatal(err)
			}
			if n, err := tc.count(dir); err != nil || n != 1 {
				data, _ := os.ReadFile(filepath.Join(dir, tc.file))
				t.Errorf("read %d entries (%v), want the appended one:\n%s", n, err, data)
			}
		})
	}
}
