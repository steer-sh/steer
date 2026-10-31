package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSetRulesCreatesTheFileAndTheSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "CLAUDE.md")

	if err := SetRules(path, []string{"rule A", "rule B"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	want := "## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n- rule B\n<!-- end-managed-by-steer -->\n"
	if got := read(t, path); got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

func TestSetRulesKeepsTheSurroundingDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	write(t, path, "# Project\n\nSome prose.\n\n## Steer Rules\n<!-- managed-by-steer -->\n- old rule\n\n## Other\n\nMore prose.\n")

	if err := SetRules(path, []string{"new rule"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	got := read(t, path)
	want := "# Project\n\nSome prose.\n\n## Steer Rules\n<!-- managed-by-steer -->\n- new rule\n<!-- end-managed-by-steer -->\n\n## Other\n\nMore prose.\n"
	if got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

func TestSetRulesEmptyRemovesTheSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	write(t, path, "# Project\n\n## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n\n## Other\n\nText.\n")

	if err := SetRules(path, nil, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	got := read(t, path)
	if strings.Contains(got, steerRulesHeader) || strings.Contains(got, "rule A") {
		t.Fatalf("expected the section gone, got:\n%q", got)
	}
	if !strings.Contains(got, "# Project") || !strings.Contains(got, "## Other") {
		t.Fatalf("expected the surrounding document kept, got:\n%q", got)
	}
}

// A missing file and an empty set describe the same state, so nothing is written.
func TestSetRulesEmptyWritesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	if err := SetRules(path, nil, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file at %s", path)
	}
}

func TestSetRulesIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	if err := SetRules(path, []string{"rule A", " rule A ", "", "rule B"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	first := read(t, path)

	if err := SetRules(path, []string{"rule A", "rule B"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if second := read(t, path); second != first {
		t.Fatalf("second write differs:\nfirst:\n%q\nsecond:\n%q", first, second)
	}
	if strings.Count(first, "- rule A") != 1 {
		t.Fatalf("expected one rule A line:\n%q", first)
	}
}

func TestHasRuleReadsTheSectionOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	write(t, path, "- outside rule\n\n## Steer Rules\n<!-- managed-by-steer -->\n- inside rule\n")

	inside, err := HasRule(path, "inside rule")
	if err != nil {
		t.Fatalf("HasRule: %v", err)
	}
	if !inside {
		t.Fatal("expected the section rule to be found")
	}

	outside, err := HasRule(path, "outside rule")
	if err != nil {
		t.Fatalf("HasRule: %v", err)
	}
	if outside {
		t.Fatal("a line outside the section is not a managed rule")
	}
}

// SectionRules returns the section's bullets in order, and nothing from the
// marker, a non-bullet line, or the sections around it.
func TestSectionRulesReadsOnlyTheSectionsBullets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	write(t, path, "# Intro\n- outside rule\n\n## Steer Rules\n<!-- managed-by-steer -->\n- rule B\nnot a bullet\n  - rule A\n\n## Later\n- after rule\n")

	got, err := SectionRules(path)
	if err != nil {
		t.Fatalf("SectionRules: %v", err)
	}
	if strings.Join(got, "|") != "rule B|rule A" {
		t.Fatalf("got %q", got)
	}

	missing, err := SectionRules(filepath.Join(t.TempDir(), "absent.md"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("a missing file carries no rules: %q, %v", missing, err)
	}
}

// A document that is a symlink keeps the link; the section lands in its target.
func TestSetRulesWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "source", "AGENTS.md")
	link := filepath.Join(dir, "harness", "AGENTS.md")
	for _, d := range []string{filepath.Dir(target), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	write(t, target, "# Project\n")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := SetRules(link, []string{"rule A"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
	want := "# Project\n\n## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n<!-- end-managed-by-steer -->\n"
	if got := read(t, target); got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

// A symlink whose target is missing is refused rather than replaced by a file.
func TestSetRulesRefusesADanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "AGENTS.md")
	if err := os.Symlink(filepath.Join(dir, "missing.md"), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := SetRules(link, []string{"rule A"}, ""); err == nil {
		t.Fatal("SetRules wrote through a dangling symlink")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
}

// The temporary file writeAtomic renames from must not be left in the directory.
func TestSetRulesLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	if err := SetRules(path, []string{"rule A"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "CLAUDE.md" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}

// A link the repository carries is refused when it resolves outside the bound;
// the file it names is left as it was.
func TestSetRulesRefusesALinkOutsideItsBound(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "notes.md")
	write(t, outside, "# Not the repository's\n")
	link := filepath.Join(repo, "CLAUDE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := SetRules(link, []string{"rule A"}, repo); err == nil {
		t.Fatal("SetRules wrote through a link that leaves its bound")
	}
	if got := read(t, outside); got != "# Not the repository's\n" {
		t.Fatalf("the outside file changed:\n%q", got)
	}
}

// A link that stays inside the bound, such as CLAUDE.md naming AGENTS.md, is
// still written at its target. The path is relative, as a project document's is.
func TestSetRulesFollowsALinkInsideItsBound(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	target := filepath.Join(repo, "AGENTS.md")
	write(t, target, "# Project\n")
	link := filepath.Join(repo, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := SetRules("CLAUDE.md", []string{"rule A"}, repo); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
	want := "# Project\n\n## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n<!-- end-managed-by-steer -->\n"
	if got := read(t, target); got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

// A section written before EndMarker ends at the next level-1 or level-2
// heading, and gains the marker; the document after it is kept whole.
func TestSetRulesKeepsWhatFollowsALegacySection(t *testing.T) {
	for _, tail := range []string{"# Appendix\n\nkeep me\n", "## Other\n\nkeep me\n"} {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		write(t, path, "# Top\n\n## Steer Rules\n<!-- managed-by-steer -->\n- old rule\n\n"+tail)

		if err := SetRules(path, []string{"new rule"}, ""); err != nil {
			t.Fatalf("SetRules: %v", err)
		}
		want := "# Top\n\n## Steer Rules\n<!-- managed-by-steer -->\n- new rule\n<!-- end-managed-by-steer -->\n\n" + tail
		if got := read(t, path); got != want {
			t.Fatalf("want:\n%q\ngot:\n%q", want, got)
		}
	}
}

// Prose, a lower heading or a fence in a section without EndMarker may be the
// user's: SetRules refuses, names the line, and leaves the document unchanged,
// for a removal as for a replacement.
func TestSetRulesRefusesAnAmbiguousLegacySection(t *testing.T) {
	for _, tail := range []string{"notes I added\n", "### Sub\n", "```\ncode\n```\n"} {
		for _, rules := range [][]string{{"new rule"}, nil} {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			before := "# Top\n\n## Steer Rules\n<!-- managed-by-steer -->\n- old rule\n\n" + tail
			write(t, path, before)

			err := SetRules(path, rules, "")
			if err == nil || !strings.Contains(err.Error(), "line 7") || !strings.Contains(err.Error(), EndMarker) {
				t.Fatalf("tail %q rules %q: want a refusal naming line 7 and the marker, got %v", tail, rules, err)
			}
			if got := read(t, path); got != before {
				t.Fatalf("tail %q: the document changed:\n%q", tail, got)
			}
		}
	}
}

// Everything after EndMarker is the user's, bullets included, and a repeated
// sync writes the same bytes.
func TestSetRulesKeepsWhatFollowsTheEndMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := SetRules(path, []string{"rule A"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	write(t, path, read(t, path)+"\nappended prose\n- my own bullet\n### Sub\n")

	if err := SetRules(path, []string{"rule B"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	first := read(t, path)
	want := "## Steer Rules\n<!-- managed-by-steer -->\n- rule B\n<!-- end-managed-by-steer -->\n\nappended prose\n- my own bullet\n### Sub\n"
	if first != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, first)
	}
	if err := SetRules(path, []string{"rule B"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if second := read(t, path); second != first {
		t.Fatalf("second write differs:\n%q", second)
	}
	if got, _ := SectionRules(path); strings.Join(got, "|") != "rule B" {
		t.Fatalf("SectionRules read past the marker: %q", got)
	}

	if err := SetRules(path, nil, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if got := read(t, path); got != "appended prose\n- my own bullet\n### Sub\n" {
		t.Fatalf("removal kept the marker or lost the tail:\n%q", got)
	}
}

// A legacy section ends at its first heading even when a second Steer Rules
// section further down has a marker; the text between them is the user's.
func TestSetRulesStopsAtASecondSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	tail := "## Other\n\nkeep me\n\n## Steer Rules\n- copy\n<!-- end-managed-by-steer -->\n"
	write(t, path, "## Steer Rules\n- old rule\n\n"+tail)

	if err := SetRules(path, []string{"new rule"}, ""); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	want := "## Steer Rules\n<!-- managed-by-steer -->\n- new rule\n<!-- end-managed-by-steer -->\n\n" + tail
	if got := read(t, path); got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

// An EndMarker past the heading that ends a section without one may belong to
// the section or to an example below it, so SetRules refuses either way rather
// than strand a rule or delete what lies between.
func TestSetRulesRefusesAMarkerPastTheSectionsHeading(t *testing.T) {
	for _, tail := range []string{
		"## Notes\n- stale rule\n<!-- end-managed-by-steer -->\n",
		"# Appendix\n\n```\n<!-- end-managed-by-steer -->\n```\n",
	} {
		for _, rules := range [][]string{{"new rule"}, nil} {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			before := "## Steer Rules\n<!-- managed-by-steer -->\n- old rule\n" + tail
			write(t, path, before)

			err := SetRules(path, rules, "")
			if err == nil || !strings.Contains(err.Error(), "past the heading on line 4") {
				t.Fatalf("tail %q rules %q: want a refusal naming the heading, got %v", tail, rules, err)
			}
			if got := read(t, path); got != before {
				t.Fatalf("tail %q: the document changed:\n%q", tail, got)
			}
		}
	}
}
