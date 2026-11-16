package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

func withCmdProjectTempDir(t *testing.T) string {
	t.Helper()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	return tempDir
}

// installFactoryGuards installs two guards an earlier Steer installed from its
// preset catalog, as an upgraded install still holds them.
func installFactoryGuards(t *testing.T, scope string) {
	t.Helper()
	for _, name := range []string{"test-guard-a", "test-guard-b"} {
		addOrphanFactoryHook(t, scope, name, name, name+" rule")
	}
}

func addOrphanFactoryHook(t *testing.T, scope, hookName, sourceID, rule string) string {
	t.Helper()

	_, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: hookName,
		FileName: hookName + ".py",
		Rule:     rule,
		Content:  []byte("#!/usr/bin/env python3\n\ndef main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{
			Source:        claude.SourceFactory,
			SourceID:      sourceID,
			SourceVersion: "v0-orphan",
			PromptVersion: "orphan-prompt",
			HookVersion:   "orphan-hook",
		},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook orphan returned error: %v", err)
	}

	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		t.Fatalf("GetHooksDir returned error: %v", err)
	}
	return filepath.Join(hooksDir, hookName+".py")
}
