package shellword

import "testing"

// TestADoubleQuotedHeadSplits: the Windows form is read on every platform, so a
// settings.json copied from a Windows machine still names one path.
// pins existing behaviour
func TestADoubleQuotedHeadSplits(t *testing.T) {
	head, rest, ok := Split(`"C:/Program Files/steer.exe" commit-gate backfill`)
	if !ok {
		t.Fatal("a double-quoted head did not split")
	}
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
	head, rest, ok := Split(Quote(path) + " commit-gate backfill")
	if !ok {
		t.Fatal("a quoted path did not split")
	}
	if head != path {
		t.Fatalf("head = %q, want %q", head, path)
	}
	if rest != "commit-gate backfill" {
		t.Fatalf("rest = %q, want %q", rest, "commit-gate backfill")
	}
}

// TestBothPlatformFormsRoundTrip runs the encoder for each platform on this host,
// because the form a registration carries is the writing machine's and the
// reading machine's platform says nothing about it.
func TestBothPlatformFormsRoundTrip(t *testing.T) {
	paths := []string{
		"/usr/local/bin/steer",
		"/Users/two words/.steer/hooks/steer-hooks.py",
		"/Users/o'brien/.steer/hooks/steer-hooks.py",
		`/Users/a$b`,
		"/Users/back\\slash",
		"C:/Program Files/steer/steer-hooks.py",
		// A word Quote leaves bare may not begin with a quote character, or Split
		// reads the wrong form. Only a malformed USERPROFILE names one on Windows.
		"'steer-hooks.py",
		"'quoted'",
	}
	for _, goos := range []string{"linux", "windows"} {
		for _, path := range paths {
			if err := encodableOn(goos, path); err != nil {
				continue
			}
			command := quoteOn(goos, path) + " --harness codex"
			head, rest, ok := Split(command)
			if !ok {
				t.Fatalf("%s: %q did not split", goos, command)
			}
			if head != path {
				t.Fatalf("%s: head = %q, want %q", goos, head, path)
			}
			if rest != "--harness codex" {
				t.Fatalf("%s: rest = %q", goos, rest)
			}
		}
	}
}

// TestAnUnencodablePathIsRefusedRatherThanChanged: the Windows branch has no
// escape for a double quote, and dropping the character would name a different
// file. Encodable is the only place that decides, and Quote never rewrites.
func TestAnUnencodablePathIsRefusedRatherThanChanged(t *testing.T) {
	quoted := `C:/od"d/steer-hooks.py`
	if err := encodableOn("windows", quoted); err == nil {
		t.Fatal("a Windows path holding a double quote must be refused")
	}
	if err := encodableOn("linux", quoted); err != nil {
		t.Fatalf("POSIX quoting handles a double quote: %v", err)
	}
	if head, _, ok := Split(quoteOn("linux", quoted) + " x"); !ok || head != quoted {
		t.Fatalf("POSIX round trip returned %q, ok=%v", head, ok)
	}
	for _, goos := range []string{"linux", "windows"} {
		if err := encodableOn(goos, "/a\x00b"); err == nil {
			t.Fatalf("%s: a NUL byte must be refused", goos)
		}
	}
}

// TestSplitRefusesAWordItCouldNotHaveWritten: a truncated head names a file the
// caller never asked for, so a command outside the written forms has no head at
// all rather than a partial one.
func TestSplitRefusesAWordItCouldNotHaveWritten(t *testing.T) {
	cases := []string{
		`'/Users/two words/steer`,
		`"C:/Program Files/steer.exe`,
		`'/Users/two words/steer'x commit-gate review`,
		`"C:/Program Files/steer.exe"x commit-gate review`,
		"",
	}
	for _, command := range cases {
		head, rest, ok := Split(command)
		if ok {
			t.Fatalf("%q split to %q", command, head)
		}
		if head != "" || rest != "" {
			t.Fatalf("%q yielded head %q and rest %q, want neither", command, head, rest)
		}
	}
}
