package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// instrumentFixtures is the corpus the adapter's instrument digests, in order.
// It is separate from the behaviour tests' fixtures so that adding a case to one
// of those does not read as a change to what the adapter emits.
var instrumentFixtures = []string{
	"bare-string-content.jsonl", "bash-execution.jsonl", "branched.jsonl",
	"custom.jsonl", "fork-parent.jsonl", "fork-child.jsonl", "header-only.jsonl",
	"malformed.jsonl", "normal.jsonl", "typeless-tail.jsonl",
	"unknown-entries.jsonl", "unlinkable.jsonl",
}

func TestInstrumentMatchesFixtureReplay(t *testing.T) {
	dir := filepath.Join("testdata", "instrument")
	parent, err := filepath.Abs(filepath.Join(dir, "fork-parent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range instrumentFixtures {
		path := filepath.Join(dir, name)
		// The fork child's header names its parent by absolute path, so the
		// fixture ships a placeholder and is materialized per run.
		if name == "fork-child.jsonl" {
			path = materializeFork(t, path, parent)
		}
		paths = append(paths, path)
	}
	digest, err := transcript.ReplayDigest(New(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := "pi:" + digest
	if instrument != want {
		t.Fatalf("instrument = %q; the replay says %q.\n"+
			"An adapter change that moves what a classifier reads must move the "+
			"instrument, so update the constant deliberately.", instrument, want)
	}
	if !strings.HasPrefix(instrument, string(New().Harness())+":") {
		t.Errorf("instrument %q does not name its harness", instrument)
	}
}

// materializeFork writes the fork fixture with its parent path filled in and
// returns the copy's path.
func materializeFork(t *testing.T, fixture, parent string) string {
	t.Helper()
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fork-child.jsonl")
	body = []byte(strings.ReplaceAll(string(body), "FORK_PARENT_PATH", parent))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
