package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withProjectTempDir(t *testing.T) string {
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

func TestLoadManifestBackwardCompatibility(t *testing.T) {
	withProjectTempDir(t)

	hooksDir := filepath.Join(".claude", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}

	legacy := `{
  "hooks": {
    "legacy-hook": {
      "file": "legacy-hook.py",
      "rule": "block TODO comments",
      "created": "2025-01-01"
    }
  }
}`
	manifestPath := filepath.Join(hooksDir, ManifestName)
	if err := os.WriteFile(manifestPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("failed to write legacy manifest: %v", err)
	}

	manifest, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}

	entry, ok := manifest.Hooks["legacy-hook"]
	if !ok {
		t.Fatalf("expected legacy-hook entry to exist")
	}
	if entry.File != "legacy-hook.py" {
		t.Fatalf("unexpected file value: %s", entry.File)
	}
	if entry.Rule != "block TODO comments" {
		t.Fatalf("unexpected rule value: %s", entry.Rule)
	}
	if entry.Created != "2025-01-01" {
		t.Fatalf("unexpected created value: %s", entry.Created)
	}
	if entry.Updated != "" || entry.Source != "" || entry.PromptVersion != "" || entry.HookVersion != "" {
		t.Fatalf("expected new fields to remain empty for legacy entries")
	}

	if err := SaveManifest(ProjectScope, manifest); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
	}
	saved, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("failed to read saved manifest: %v", err)
	}
	if strings.Contains(string(saved), `"updated"`) {
		t.Fatalf("unexpected updated field in untouched legacy entry")
	}
}

func TestManifestRoundTripWithMetadata(t *testing.T) {
	withProjectTempDir(t)

	manifest := &Manifest{
		Hooks: map[string]ManifestEntry{
			"factory-hook": {
				File:          "factory-hook.py",
				Rule:          "never use var",
				Created:       "2026-01-01",
				Updated:       "2026-01-02",
				Source:        SourceFactory,
				SourceID:      "javascript-strict-equality",
				SourceVersion: "v1.0.0",
				PromptVersion: "factory-prompt-v1",
				PromptModules: []string{"simple-regex"},
				HookVersion:   "factory-hook-v1",
				Model:         "claude-opus-4-6",
				SkillVersion:  "1.2.3",
				RuleHash:      HashRule("never use var"),
				SelfTest: &SelfTestSummary{
					Passed:       true,
					PositivePass: 2,
					PositiveFail: 0,
					NegativePass: 2,
					NegativeFail: 0,
				},
			},
		},
	}

	if err := SaveManifest(ProjectScope, manifest); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
	}

	loaded, err := LoadManifest(ProjectScope)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}

	entry, ok := loaded.Hooks["factory-hook"]
	if !ok {
		t.Fatalf("expected factory-hook entry to exist after reload")
	}
	if entry.Source != SourceFactory || entry.SourceID != "javascript-strict-equality" || entry.SourceVersion != "v1.0.0" {
		t.Fatalf("source metadata did not round-trip: %+v", entry)
	}
	if entry.PromptVersion != "factory-prompt-v1" || entry.HookVersion != "factory-hook-v1" {
		t.Fatalf("version metadata did not round-trip: %+v", entry)
	}
	if len(entry.PromptModules) != 1 || entry.PromptModules[0] != "simple-regex" {
		t.Fatalf("prompt_modules did not round-trip: %+v", entry.PromptModules)
	}
	if entry.SelfTest == nil || !entry.SelfTest.Passed || entry.SelfTest.PositivePass != 2 || entry.SelfTest.NegativePass != 2 {
		t.Fatalf("self_test did not round-trip: %+v", entry.SelfTest)
	}
}

func TestHashRuleStability(t *testing.T) {
	ruleA := "never use   var   declarations"
	ruleB := "never use var declarations"
	ruleC := "never use var declarations\n"

	hashA := HashRule(ruleA)
	hashB := HashRule(ruleB)
	hashC := HashRule(ruleC)

	if hashA == "" || hashB == "" || hashC == "" {
		t.Fatalf("hash should never be empty")
	}
	if hashA != hashB || hashB != hashC {
		t.Fatalf("expected whitespace-normalized hashes to match: %s %s %s", hashA, hashB, hashC)
	}
}
