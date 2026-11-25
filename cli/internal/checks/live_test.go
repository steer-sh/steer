package checks

import (
	"os"
	"path/filepath"
	"testing"
)

// Every repository directory's streams are read, and a repository with only a
// validation still counts as holding check evidence.
func TestReadLiveEvidenceReadsEveryRepositoryAndMarksValidatedOnes(t *testing.T) {
	dir := t.TempDir()
	write := func(key, name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, key), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, key, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("aaaa", executionsName, `{"repo":"/w/one","check_id":"x","outcome":"passed"}`+"\nnot json\n")
	write("aaaa", runsName, `{"repo":"/w/one","run_id":"r1","disposition":"block"}`+"\n")
	write("bbbb", validationsName, `{"repo":"/w/two","check_id":"y","verdict":"validated"}`+"\n")
	if err := os.WriteFile(filepath.Join(dir, adoptionsName), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev, err := readLiveEvidenceIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Executions) != 1 || ev.Executions[0].CheckID != "x" {
		t.Errorf("executions = %+v, want the one parseable record", ev.Executions)
	}
	if len(ev.Runs) != 1 || ev.Runs[0].RunID != "r1" {
		t.Errorf("runs = %+v, want r1", ev.Runs)
	}
	if !ev.Repos["/w/one"] || !ev.Repos["/w/two"] || len(ev.Repos) != 2 {
		t.Errorf("repos = %v, want /w/one and /w/two", ev.Repos)
	}

	empty, err := readLiveEvidenceIn(filepath.Join(dir, "absent"))
	if err != nil || len(empty.Repos) != 0 {
		t.Errorf("a missing state directory = %+v, %v; want empty evidence and no error", empty, err)
	}
}
