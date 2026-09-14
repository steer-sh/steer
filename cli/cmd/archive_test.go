package cmd

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
)

// archiveRepo makes a Git repository with fake homes and returns its root.
func archiveRepo(t *testing.T) string {
	t.Helper()
	fakeHomes(t)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	root, err := repoRootOf(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runArchiveCmd(t *testing.T, c *cobra.Command, runScope string, args []string, flags map[string]string) (string, error) {
	t.Helper()
	old := scope
	scope = runScope
	t.Cleanup(func() { scope = old })
	for name, value := range flags {
		if err := c.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Flags().Set(name, "") })
	}
	var out bytes.Buffer
	c.SetOut(&out)
	t.Cleanup(func() { c.SetOut(nil) })
	err := c.RunE(c, args)
	return out.String(), err
}

func writeArchived(t *testing.T, scope, dir, kind, id, reason string, manifest any, missing ...string) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(dir, archive.Entry{
		Tier: kind, ID: id, Scope: scope, Reason: reason, Manifest: raw, Missing: missing,
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveListReadsTheThreeStores(t *testing.T) {
	root := archiveRepo(t)
	project, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	global, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	writeArchived(t, claude.ProjectScope, project, "guard", "project-guard", "blocks too much", claude.ManifestEntry{}, claude.ArchiveMissingGuardFile)
	writeArchived(t, claude.GlobalScope, global, "instruction", "global-rule", "", claude.RuleEntry{Text: "a rule"})
	writeArchived(t, claude.ProjectScope, checks.Dir(root), "check", "project-check", "flaky on CI", checks.Declaration{ID: "project-check"})

	out, err := runArchiveCmd(t, archiveListCmd, claude.ProjectScope, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"guard  project-guard  project", "reason: blocks too much", "missing: guard file",
		"instruction  global-rule  global",
		"check  project-check  project", "reason: flaky on CI",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}
}

func TestArchiveRestoreSelectsAcrossTheHookAndCheckStores(t *testing.T) {
	root := archiveRepo(t)
	if _, err := claude.InstallManagedRule(claude.ProjectScope, "shared", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveManagedRule(claude.ProjectScope, "shared", ""); err != nil {
		t.Fatal(err)
	}
	writeArchived(t, claude.ProjectScope, checks.Dir(root), "check", "shared", "", checks.Declaration{ID: "shared"})

	if _, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, nil); err == nil || !strings.Contains(err.Error(), "--tier") {
		t.Fatalf("an id held by a rule and a check was restored without --tier: %v", err)
	}
	if _, err := runArchiveCmd(t, archiveRestoreCmd, claude.GlobalScope, []string{"shared"}, nil); err == nil || !strings.Contains(err.Error(), "no archive entry") {
		t.Fatalf("a global restore read the project stores: %v", err)
	}

	out, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, map[string]string{"tier": "prose"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Rules["shared"].Text != "resolve a config path from the repo root" {
		t.Fatalf("restored rule = %+v", manifest.Rules["shared"])
	}
	if !strings.Contains(out, "steer fix register --supersedes <record-id>") {
		t.Fatalf("restore output lacks the record pointer:\n%s", out)
	}
}
