package steerbin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsurePlacesTheRunningBinary copies this test binary to the stable path and
// checks the file is there and runnable. A second call over the same home is a
// no-op, which is what an install registering three channels relies on.
func TestEnsurePlacesTheRunningBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".steer", "bin", "steer")
	if path != want {
		t.Fatalf("stable path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("mode = %v, want the executable bit set", info.Mode())
	}
	if Resolve(Command("commit-gate review")) != Resolvable {
		t.Fatal("the command naming the stable path does not resolve")
	}
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
}

// TestResolveSeesAMissingCommand pins the reading the whole posture change rests
// on: a registration naming a command that is not there is missing, not armed.
func TestResolveSeesAMissingCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := Resolve("/nowhere/steer commit-gate review"); got != Missing {
		t.Fatalf("absolute missing command = %q, want %q", got, Missing)
	}
	if got := Resolve("steer-not-real-xyz commit-gate review"); got != Missing {
		t.Fatalf("bare missing command = %q, want %q", got, Missing)
	}
	if got := Resolve(""); got != Unknown {
		t.Fatalf("empty command = %q, want %q", got, Unknown)
	}
	dir := t.TempDir()
	if got := Resolve(dir + " review"); got != Missing {
		t.Fatalf("a directory = %q, want %q", got, Missing)
	}
}

// TestRelinkKeepsArgumentsAndForeignCommands pins both halves: steer's own
// command is repointed with every flag intact, and a command that is not steer's
// is left alone.
func TestRelinkKeepsArgumentsAndForeignCommands(t *testing.T) {
	path := "/home/j/.steer/bin/steer"
	got := Relink("steer commit-gate review --provider gemini", path)
	want := path + " commit-gate review --provider gemini"
	if got != want {
		t.Fatalf("relinked = %q, want %q", got, want)
	}
	if got := Relink(got, path); got != want {
		t.Fatalf("second relink = %q, want %q", got, want)
	}
	// A Windows registration names steer.exe. The stem is what identifies the
	// command as steer's own, so it relinks on either platform.
	if got := Relink("/opt/steer/steer.exe commit-gate backfill", path); got != path+" commit-gate backfill" {
		t.Fatalf("an .exe command relinked to %q", got)
	}
	// A registration written on Windows, read on this machine: the name is still
	// steer's, and filepath.Base alone would not find it behind a backslash.
	windows := `"C:\\Users\\j\\.steer\\bin\\steer.exe" commit-gate review`
	if got := Relink(windows, path); got != path+" commit-gate review" {
		t.Fatalf("a windows registration relinked to %q", got)
	}
	foreign := "/usr/local/bin/my-wrapper commit-gate review"
	if got := Relink(foreign, path); got != foreign {
		t.Fatalf("foreign command rewritten to %q", got)
	}
}

// TestADoubleQuotedHeadSplits pins that the form Quote writes on Windows parses
// back on every platform: a settings.json travels between machines, and a head
// this reader cannot split is a command it would relink into a truncated path.
func TestADoubleQuotedHeadSplits(t *testing.T) {
	head, rest := split(`"C:/Program Files/steer.exe" commit-gate backfill`)
	if head != "C:/Program Files/steer.exe" {
		t.Fatalf("head = %q", head)
	}
	if rest != "commit-gate backfill" {
		t.Fatalf("rest = %q", rest)
	}
}

// TestASpacedPathSurvivesQuotingAndSplitting: a home directory with a space is
// one shell word going out and one command going back in, or a relink of an
// already-relinked registration would truncate the path.
func TestASpacedPathSurvivesQuotingAndSplitting(t *testing.T) {
	path := "/Users/two words/o'brien/.steer/bin/steer"
	command := Quote(path) + " commit-gate backfill"
	head, rest := split(command)
	if head != path {
		t.Fatalf("head = %q, want %q", head, path)
	}
	if rest != "commit-gate backfill" {
		t.Fatalf("rest = %q, want %q", rest, "commit-gate backfill")
	}
	if got := Relink(command, path); got != command {
		t.Fatalf("relink of a quoted command = %q, want %q", got, command)
	}
}

func TestStripFlagsRemovesTheFlagAndItsValue(t *testing.T) {
	quotedPath := Quote("/home/a b/.steer/bin/steer")
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "a flag and its value",
			command: "steer commit-gate review --provider agy --model gemini-3.7-flash-high",
			want:    "steer commit-gate review",
		},
		{
			name:    "the equals form",
			command: "steer commit-gate review --model=codex-5 --provider=codex",
			want:    "steer commit-gate review",
		},
		{
			name:    "an unnamed flag is kept",
			command: "steer commit-gate review --provider agy --harness codex",
			want:    "steer commit-gate review --harness codex",
		},
		{
			name:    "a value holding a space is one word",
			command: "steer commit-gate review --model 'a b' --harness codex",
			want:    "steer commit-gate review --harness codex",
		},
		{
			name:    "a value beginning with a hyphen is still the value",
			command: "steer commit-gate review --model -3.7 --harness codex",
			want:    "steer commit-gate review --harness codex",
		},
		{
			name:    "a quoted path keeps its quoting",
			command: quotedPath + " commit-gate review --model x",
			want:    quotedPath + " commit-gate review",
		},
		{
			name:    "nothing to strip is returned unchanged",
			command: "steer commit-gate review",
			want:    "steer commit-gate review",
		},
		{
			name:    "a wrapper script is never rewritten",
			command: "/usr/local/bin/review-wrapper commit-gate review --model x",
			want:    "/usr/local/bin/review-wrapper commit-gate review --model x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripFlags(tc.command, "--provider", "--model"); got != tc.want {
				t.Fatalf("StripFlags = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHasFlagsReadsWhatStripFlagsWouldRemove(t *testing.T) {
	if !HasFlags("steer commit-gate review --model x", "--model") {
		t.Fatal("a registration carrying --model must read as flagged")
	}
	if HasFlags("steer commit-gate review", "--model") {
		t.Fatal("a bare registration must not read as flagged")
	}
	if HasFlags("/usr/local/bin/wrapper --model x", "--model") {
		t.Fatal("a wrapper script is not steer's to read as flagged")
	}
}
