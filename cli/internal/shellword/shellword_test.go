package shellword

import "testing"

// TestADoubleQuotedHeadSplits: the Windows form is read on every platform, so a
// settings.json copied from a Windows machine still names one path.
// pins existing behaviour
func TestADoubleQuotedHeadSplits(t *testing.T) {
	head, rest := Split(`"C:/Program Files/steer.exe" commit-gate backfill`)
	if head != "C:/Program Files/steer.exe" {
		t.Fatalf("head = %q", head)
	}
	if rest != "commit-gate backfill" {
		t.Fatalf("rest = %q", rest)
	}
}

// TestASpacedPathSurvivesQuotingAndSplitting: a home directory with a space is
// one shell word going out and one path coming back in, or a relink of an
// already-relinked registration would truncate the path.
// pins existing behaviour
func TestASpacedPathSurvivesQuotingAndSplitting(t *testing.T) {
	path := "/Users/two words/o'brien/.steer/bin/steer"
	head, rest := Split(Quote(path) + " commit-gate backfill")
	if head != path {
		t.Fatalf("head = %q, want %q", head, path)
	}
	if rest != "commit-gate backfill" {
		t.Fatalf("rest = %q, want %q", rest, "commit-gate backfill")
	}
}
