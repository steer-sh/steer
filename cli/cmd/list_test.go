package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

// runList writes the given guard files and manifest to a throwaway global store
// and returns what steer list prints.
func runList(t *testing.T, verbose bool, files []string, entries map[string]claude.ManifestEntry) string {
	t.Helper()
	tempSettingsHome(t)
	oldScope, oldVerbose := scope, listVerbose
	scope, listVerbose = claude.GlobalScope, verbose
	t.Cleanup(func() { scope, listVerbose = oldScope, oldVerbose })

	if err := claude.EnsureDirectories(claude.GlobalScope); err != nil {
		t.Fatalf("EnsureDirectories: %v", err)
	}
	dir, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		t.Fatalf("GetHooksDir: %v", err)
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("# guard\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if err := claude.SaveManifest(claude.GlobalScope, &claude.Manifest{Hooks: entries}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return captureStdout(t, func() {
		if err := listCmd.RunE(listCmd, nil); err != nil {
			t.Fatalf("list: %v", err)
		}
	})
}

// A guard whose id differs from its file stem lists under that id, with its rule
// and its provenance.
func TestListShowsTheEntryThatOwnsTheFile(t *testing.T) {
	out := runList(t, true, []string{"block-push-hook.py"}, map[string]claude.ManifestEntry{
		"no-push": {File: "block-push-hook.py", Rule: "never push", Source: "preset", Model: "stub-model"},
	})
	for _, want := range []string{"1. no-push", "Rule: never push", "source=preset", "model=stub-model"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no rule recorded") {
		t.Errorf("the owning entry's rule was not found:\n%s", out)
	}
}

// Rows are ordered by the id they show.
func TestListOrdersByShownID(t *testing.T) {
	out := runList(t, false, []string{"a-file.py", "b-file.py"}, map[string]claude.ManifestEntry{
		"z-guard": {File: "a-file.py", Rule: "z rule"},
		"m-guard": {File: "b-file.py", Rule: "m rule"},
	})
	if m, z := strings.Index(out, "m-guard"), strings.Index(out, "z-guard"); m < 0 || z < 0 || m > z {
		t.Errorf("rows are not ordered by id:\n%s", out)
	}
}

// A file no entry owns shows its stem and no rule, even when an entry named for
// that stem owns a different file.
func TestListGivesAnUnownedFileNoRule(t *testing.T) {
	out := runList(t, false, []string{"orphan.py", "moved.py", "elsewhere.py"}, map[string]claude.ManifestEntry{
		"moved": {File: "elsewhere.py", Rule: "moved rule"},
	})
	for file, want := range map[string]string{
		"orphan.py":    "(no rule recorded)",
		"moved.py":     "(no rule recorded)",
		"elsewhere.py": "moved rule",
	} {
		if got := ruleOfRow(t, out, file); got != want {
			t.Errorf("%s shows rule %q, want %q:\n%s", file, got, want, out)
		}
	}
}

// ruleOfRow returns the rule printed on the row whose path ends in file.
func ruleOfRow(t *testing.T, out, file string) string {
	t.Helper()
	_, row, found := strings.Cut(out, "/"+file)
	if !found {
		t.Fatalf("no row for %s:\n%s", file, out)
	}
	_, rest, found := strings.Cut(row, "Rule: ")
	if !found {
		t.Fatalf("the row for %s has no rule:\n%s", file, out)
	}
	rule, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(rule)
}
