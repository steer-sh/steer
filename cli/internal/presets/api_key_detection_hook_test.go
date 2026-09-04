package presets

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

func runAPIKeyHook(t *testing.T, path, dir, tool, content string) bool {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_name": tool,
		"tool_input": map[string]any{
			"file_path": "config.js",
			"content":   content,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", path)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("running API-key hook: %v", err)
	}
	if len(output) == 0 {
		return false
	}
	var reply struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(output, &reply); err != nil {
		t.Fatalf("parsing hook reply %q: %v", output, err)
	}
	return reply.Decision == "block"
}

func TestAPIKeyDetectionScopesExemptionsToTheMatchedExpression(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("hooks", "api-key-detection.py"))
	if err != nil {
		t.Fatal(err)
	}
	dir := withPresetProjectTempDir(t)

	tests := []struct {
		name    string
		tool    string
		content string
		blocked bool
	}{
		{
			name:    "secure reference does not exempt fallback",
			tool:    "Write",
			content: `const key = process.env.STRIPE_KEY || "sk_live_abcdefghijklmnopqrstuvwx";`,
			blocked: true,
		},
		{
			name:    "placeholder does not exempt later key",
			tool:    "Write",
			content: `const keys = ["sk_live_xxxxxxxxxxxxxxxxxxxxxxxx", "AKIAABCDEFGHIJKLMNOP"];`,
			blocked: true,
		},
		{
			name:    "generic secret assignment",
			tool:    "Write",
			content: `const api_key = "abcdefghijklmnop";`,
			blocked: true,
		},
		{
			name:    "secure lookup containing key-like name",
			tool:    "Write",
			content: `const key = os.getenv("sk_live_abcdefghijklmnopqrstuvwx");`,
			blocked: false,
		},
		{
			name:    "secure lookup",
			tool:    "Write",
			content: `const key = process.env.STRIPE_KEY;`,
			blocked: false,
		},
		{
			name:    "placeholder value",
			tool:    "Write",
			content: `const api_key = "placeholder_value";`,
			blocked: false,
		},
		{
			name:    "irrelevant tool",
			tool:    "Read",
			content: `const key = "sk_live_abcdefghijklmnopqrstuvwx";`,
			blocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runAPIKeyHook(t, source, dir, tt.tool, tt.content); got != tt.blocked {
				t.Fatalf("blocked = %v, want %v", got, tt.blocked)
			}
		})
	}
}

func TestAPIKeyDetectionV2InstallsIntoManifestAndMaster(t *testing.T) {
	dir := withPresetProjectTempDir(t)
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	item, ok := catalog.Find("api-key-detection")
	if !ok {
		t.Fatal("api-key-detection missing from catalog")
	}
	if item.HookVersion != "factory-hook-v2" {
		t.Fatalf("hook version = %q, want factory-hook-v2", item.HookVersion)
	}
	if _, err := EnablePreset(claude.ProjectScope, catalog, item, false); err != nil {
		t.Fatal(err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Hooks["api-key-detection"].HookVersion; got != "factory-hook-v2" {
		t.Fatalf("manifest hook version = %q, want factory-hook-v2", got)
	}

	master := filepath.Join(dir, ".steer", "hooks", "steer-hooks.py")
	if !runAPIKeyHook(t, master, dir, "Write", `const key = process.env.KEY || "AKIAABCDEFGHIJKLMNOP";`) {
		t.Fatal("consolidated master allowed the mixed secure and hardcoded expression")
	}
	if runAPIKeyHook(t, master, dir, "Write", `const key = process.env.KEY;`) {
		t.Fatal("consolidated master blocked a secure reference")
	}
}
