package cc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// instrumentFixtures is the corpus the adapter's instrument digests, in order.
// It is separate from the behaviour tests' fixtures so that adding a case to one
// of those does not read as a change to what the adapter emits.
var instrumentFixtures = []string{"session.jsonl", "headless.jsonl"}

func TestInstrumentMatchesFixtureReplay(t *testing.T) {
	var paths []string
	for _, name := range instrumentFixtures {
		paths = append(paths, filepath.Join("testdata", "instrument", name))
	}
	digest, err := transcript.ReplayDigest(New(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := "claude-code:" + digest
	if instrument != want {
		t.Fatalf("instrument = %q; the replay says %q.\n"+
			"An adapter change that moves what a classifier reads must move the "+
			"instrument, so update the constant deliberately.", instrument, want)
	}
	if !strings.HasPrefix(instrument, string(New().Harness())+":") {
		t.Errorf("instrument %q does not name its harness", instrument)
	}
}
