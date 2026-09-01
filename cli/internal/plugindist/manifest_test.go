package plugindist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The repository ships three manifests describing one plugin: Claude Code's
// marketplace, Codex's marketplace and Codex's plugin manifest. Codex copies a
// plugin root verbatim, so a field that disagrees between them installs one
// plugin under two identities. These tests compare whole documents rather than
// a modeled subset, because an unmodeled field is exactly the one that drifts.

// marketplaceOnlyKeys are the keys a marketplace entry carries that the plugin
// manifest at the plugin root cannot: the root is its own source, and strictness
// is the marketplace's decision about the entry.
var marketplaceOnlyKeys = []string{"source", "strict"}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found: no .claude-plugin/marketplace.json above the test")
		}
		dir = parent
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func soleSteerPlugin(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := readJSON(t, path)
	if doc["name"] != "steer-marketplace" {
		t.Fatalf("%s: marketplace name = %v, want steer-marketplace", path, doc["name"])
	}
	plugins, ok := doc["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("%s: want exactly 1 plugin, got %v", path, doc["plugins"])
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("%s: plugin entry is not an object", path)
	}
	if entry["name"] != "steer" {
		t.Fatalf("%s: plugin name = %v, want steer", path, entry["name"])
	}
	return entry
}

func keys(doc map[string]any) []string {
	out := make([]string, 0, len(doc))
	for k := range doc {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestBothMarketplacesAreTheSameDocument(t *testing.T) {
	root := repoRoot(t)
	cc := readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))
	codex := readJSON(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"))
	if !reflect.DeepEqual(cc, codex) {
		t.Fatalf("the two marketplaces describe different plugins:\n claude: %s\n codex:  %s",
			mustJSON(t, cc), mustJSON(t, codex))
	}
}

func TestCodexPluginManifestMatchesItsMarketplaceEntry(t *testing.T) {
	root := repoRoot(t)
	entry := soleSteerPlugin(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"))
	manifest := readJSON(t, filepath.Join(root, ".codex-plugin", "plugin.json"))

	for _, key := range marketplaceOnlyKeys {
		if _, ok := entry[key]; !ok {
			t.Errorf("marketplace entry is missing %q", key)
		}
		if value, ok := manifest[key]; ok {
			t.Errorf(".codex-plugin/plugin.json carries %q = %v; that key is the marketplace's", key, value)
		}
		delete(entry, key)
	}
	if !reflect.DeepEqual(entry, manifest) {
		t.Fatalf("plugin manifest differs from its marketplace entry:\n keys(manifest): %v\n keys(entry):    %v\n manifest: %s\n entry:    %s",
			keys(manifest), keys(entry), mustJSON(t, manifest), mustJSON(t, entry))
	}
}

func TestEveryDeclaredPluginPathExists(t *testing.T) {
	root := repoRoot(t)
	manifest := readJSON(t, filepath.Join(root, ".codex-plugin", "plugin.json"))

	hooks, ok := manifest["hooks"].(string)
	if !ok {
		t.Fatalf("plugin manifest declares no hooks path: %v", manifest["hooks"])
	}
	skills, ok := manifest["skills"].([]any)
	if !ok || len(skills) == 0 {
		t.Fatalf("plugin manifest declares no skills: %v", manifest["skills"])
	}

	paths := []string{hooks, "scripts/steer"}
	for _, skill := range skills {
		dir, ok := skill.(string)
		if !ok {
			t.Fatalf("skill entry is not a string: %v", skill)
		}
		paths = append(paths, filepath.Join(dir, "SKILL.md"))
	}
	for _, rel := range paths {
		rel = strings.TrimPrefix(rel, "./")
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("declared path %s: %v", rel, err)
		}
	}
}

func TestBundledHookResolvesEitherHarnessPluginRoot(t *testing.T) {
	root := repoRoot(t)
	var bundle struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks/hooks.json: %v", err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse hooks/hooks.json: %v", err)
	}

	entries, ok := bundle.Hooks["SessionStart"]
	if !ok || len(entries) == 0 || len(entries[0].Hooks) == 0 {
		t.Fatal("hooks/hooks.json declares no SessionStart command")
	}
	command := entries[0].Hooks[0].Command

	// Codex sets PLUGIN_ROOT and Claude Code sets CLAUDE_PLUGIN_ROOT, so one
	// bundled command reads the first and falls back to the second. The inner
	// name stays braced: Claude Code may substitute the placeholder into the
	// string instead of exporting it, and the braced form is the one every
	// version of that substitution has accepted.
	if !strings.Contains(command, "${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT}}") {
		t.Fatalf("SessionStart command does not resolve both plugin roots: %q", command)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
