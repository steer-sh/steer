package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Manifest stores hook metadata used for list/remove/CLAUDE.md cleanup.
type Manifest struct {
	Hooks map[string]ManifestEntry `json:"hooks"`
}

// ManifestEntry is metadata for one registered hook.
type ManifestEntry struct {
	File    string `json:"file"`
	Rule    string `json:"rule"`
	Created string `json:"created"`
}

// ManifestListEntry is a flattened view for list output.
type ManifestListEntry struct {
	Name    string
	File    string
	Rule    string
	Created string
}

// LoadManifest reads steer-manifest.json for the provided scope.
func LoadManifest(scope string) (*Manifest, error) {
	manifestPath, err := GetManifestFile(scope)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{Hooks: map[string]ManifestEntry{}}
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return manifest, nil
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return manifest, nil
	}

	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}
	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	return manifest, nil
}

// SaveManifest writes steer-manifest.json for the provided scope.
func SaveManifest(scope string, manifest *Manifest) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	manifestPath, err := GetManifestFile(scope)
	if err != nil {
		return err
	}

	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// AddHook adds a hook mapping to the manifest.
func AddHook(scope, hookName, fileName, rule string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}

	hookName = strings.TrimSpace(hookName)
	if hookName == "" {
		return fmt.Errorf("hook name cannot be empty")
	}
	if _, exists := manifest.Hooks[hookName]; exists {
		return fmt.Errorf("hook already registered: %s", hookName)
	}

	manifest.Hooks[hookName] = ManifestEntry{
		File:    fileName,
		Rule:    strings.TrimSpace(rule),
		Created: time.Now().Format("2006-01-02"),
	}

	return SaveManifest(scope, manifest)
}

// RemoveHook removes a hook mapping from the manifest.
func RemoveHook(scope, hookName string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}

	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	delete(manifest.Hooks, strings.TrimSpace(hookName))
	return SaveManifest(scope, manifest)
}

// GetRule returns the rule text for a hook name, or empty string when absent.
func GetRule(scope, hookName string) (string, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return "", err
	}

	entry, ok := manifest.Hooks[strings.TrimSpace(hookName)]
	if !ok {
		return "", nil
	}
	return entry.Rule, nil
}

// ListHooks returns all hooks from manifest in sorted order.
func ListHooks(scope string) ([]ManifestListEntry, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	entries := make([]ManifestListEntry, 0, len(manifest.Hooks))
	for name, entry := range manifest.Hooks {
		entries = append(entries, ManifestListEntry{
			Name:    name,
			File:    entry.File,
			Rule:    entry.Rule,
			Created: entry.Created,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}
