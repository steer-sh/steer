package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestHook(t *testing.T, path string) []byte {
	t.Helper()
	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("failed to write test hook: %v", err)
	}
	return content
}

func TestInstallManagedHookFirstTime(t *testing.T) {
	withProjectTempDir(t)

	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")

	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("InstallManagedHook returned error: %v", err)
	}
	if result.Overwritten {
		t.Fatal("expected Overwritten=false for first install")
	}
	if result.HookName != "no-var-hook" {
		t.Fatalf("unexpected hook name: %s", result.HookName)
	}

	// Verify file was written
	if _, err := os.Stat(result.HookPath); err != nil {
		t.Fatalf("hook file not found at %s: %v", result.HookPath, err)
	}

	// Verify manifest entry
	entry, exists, err := GetHookEntry(ProjectScope, "no-var-hook")
	if err != nil {
		t.Fatalf("GetHookEntry error: %v", err)
	}
	if !exists {
		t.Fatal("expected manifest entry to exist")
	}
	if entry.Rule != "never use var" {
		t.Fatalf("unexpected rule: %s", entry.Rule)
	}
}

func TestInstallManagedHookUpsertSameHook(t *testing.T) {
	withProjectTempDir(t)

	content := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")

	// First install
	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Second install with updated rule — should upsert without error
	updatedContent := []byte("#!/usr/bin/env python3\nimport sys\nimport json\n# updated\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var | use const or let",
		Content:  updatedContent,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if !result.Overwritten {
		t.Fatal("expected Overwritten=true for upsert")
	}
	if result.PreviousRule != "never use var" {
		t.Fatalf("expected previous rule to be preserved, got: %s", result.PreviousRule)
	}

	// Verify updated rule in manifest
	entry, exists, err := GetHookEntry(ProjectScope, "no-var-hook")
	if err != nil {
		t.Fatalf("GetHookEntry error: %v", err)
	}
	if !exists {
		t.Fatal("expected manifest entry to exist after upsert")
	}
	if entry.Rule != "never use var | use const or let" {
		t.Fatalf("rule not updated: %s", entry.Rule)
	}
}

func TestInstallManagedHookFileExistsNoManifest(t *testing.T) {
	withProjectTempDir(t)

	// Pre-create the hook file (simulates agent writing it before register)
	hooksDir := filepath.Join(".claude", "hooks")
	hookPath := filepath.Join(hooksDir, "no-var-hook.py")
	content := writeTestHook(t, hookPath)

	// Register should succeed even though file already exists
	result, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-var-hook",
		FileName: "no-var-hook.py",
		Rule:     "never use var",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err != nil {
		t.Fatalf("register with pre-existing file failed: %v", err)
	}
	if result.Overwritten {
		t.Fatal("expected Overwritten=false (no prior manifest entry)")
	}
}
