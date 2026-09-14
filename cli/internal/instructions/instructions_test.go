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

	if err := SetRules(path, []string{"rule A", "rule B"}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	want := "## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n- rule B\n"
	if got := read(t, path); got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

func TestSetRulesKeepsTheSurroundingDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	write(t, path, "# Project\n\nSome prose.\n\n## Steer Rules\n<!-- managed-by-steer -->\n- old rule\n\n## Other\n\nMore prose.\n")

	if err := SetRules(path, []string{"new rule"}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	got := read(t, path)
	want := "# Project\n\nSome prose.\n\n## Steer Rules\n<!-- managed-by-steer -->\n- new rule\n\n## Other\n\nMore prose.\n"
	if got != want {
		t.Fatalf("want:\n%q\ngot:\n%q", want, got)
	}
}

func TestSetRulesEmptyRemovesTheSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	write(t, path, "# Project\n\n## Steer Rules\n<!-- managed-by-steer -->\n- rule A\n\n## Other\n\nText.\n")

	if err := SetRules(path, nil); err != nil {
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

	if err := SetRules(path, nil); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file at %s", path)
	}
}

func TestSetRulesIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	if err := SetRules(path, []string{"rule A", " rule A ", "", "rule B"}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	first := read(t, path)

	if err := SetRules(path, []string{"rule A", "rule B"}); err != nil {
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

// The temporary file writeAtomic renames from must not be left in the directory.
func TestSetRulesLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	if err := SetRules(path, []string{"rule A"}); err != nil {
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
