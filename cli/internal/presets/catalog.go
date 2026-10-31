package presets

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	BundleSecurity = "security"
	BundleQuality  = "quality"

	ExpectedSecurityCount = 12
	ExpectedQualityCount  = 0
	ExpectedInternalCount = 1
)

var validBundles = map[string]struct{}{
	BundleSecurity: {},
	BundleQuality:  {},
}

//go:embed catalog.json hooks/*.py
var embeddedAssets embed.FS

// Catalog defines all bundled factory presets.
type Catalog struct {
	CatalogVersion string        `json:"catalog_version"`
	Presets        []CatalogItem `json:"presets"`
}

// CatalogItem defines one preset entry in the embedded catalog.
type CatalogItem struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Rule                  string   `json:"rule"`
	HookFile              string   `json:"hook_file"`
	Bundles               []string `json:"bundles"`
	OS                    []string `json:"os"`
	PromptVersion         string   `json:"prompt_version"`
	PromptModules         []string `json:"prompt_modules"`
	HookVersion           string   `json:"hook_version"`
	Internal              bool     `json:"internal,omitempty"`
	AutoEnableWithFactory bool     `json:"auto_enable_with_factory,omitempty"`
}

var (
	cachedCatalog *Catalog
	cachedErr     error
	loadOnce      sync.Once
)

// LoadCatalog reads and validates the embedded factory catalog.
func LoadCatalog() (*Catalog, error) {
	loadOnce.Do(func() {
		data, err := fs.ReadFile(embeddedAssets, "catalog.json")
		if err != nil {
			cachedErr = fmt.Errorf("failed to read preset catalog: %w", err)
			return
		}

		var catalog Catalog
		if err := json.Unmarshal(data, &catalog); err != nil {
			cachedErr = fmt.Errorf("failed to parse preset catalog: %w", err)
			return
		}

		if err := validateCatalog(&catalog); err != nil {
			cachedErr = err
			return
		}
		cachedCatalog = &catalog
	})

	if cachedErr != nil {
		return nil, cachedErr
	}
	return cachedCatalog, nil
}

func normalizeBundle(bundle string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(bundle))
	if normalized == "" {
		return "", nil
	}
	if _, ok := validBundles[normalized]; !ok {
		return "", fmt.Errorf("invalid bundle %q: expected %q or %q", bundle, BundleSecurity, BundleQuality)
	}
	return normalized, nil
}

func sortedUnique(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func partitionPresets(items []CatalogItem) (visible []CatalogItem, internal []CatalogItem) {
	visible = make([]CatalogItem, 0, len(items))
	internal = make([]CatalogItem, 0, len(items))
	for _, item := range items {
		if item.Internal {
			internal = append(internal, item)
			continue
		}
		visible = append(visible, item)
	}
	return visible, internal
}

func validateCatalog(catalog *Catalog) error {
	if catalog == nil {
		return fmt.Errorf("preset catalog is nil")
	}
	if strings.TrimSpace(catalog.CatalogVersion) == "" {
		return fmt.Errorf("preset catalog must include catalog_version")
	}
	_, internalPresets := partitionPresets(catalog.Presets)

	nameSet := map[string]struct{}{}
	bundleCounts := map[string]int{
		BundleSecurity: 0,
		BundleQuality:  0,
	}

	for _, item := range catalog.Presets {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return fmt.Errorf("preset entry has empty name")
		}
		if _, exists := nameSet[name]; exists {
			return fmt.Errorf("duplicate preset name %q", name)
		}
		nameSet[name] = struct{}{}

		if strings.TrimSpace(item.Description) == "" {
			return fmt.Errorf("preset %q has empty description", name)
		}
		if strings.TrimSpace(item.Rule) == "" {
			return fmt.Errorf("preset %q has empty rule", name)
		}
		if strings.TrimSpace(item.HookFile) == "" {
			return fmt.Errorf("preset %q has empty hook_file", name)
		}

		hookAssetPath := filepath.ToSlash(filepath.Join("hooks", item.HookFile))
		if _, err := fs.Stat(embeddedAssets, hookAssetPath); err != nil {
			return fmt.Errorf("preset %q hook asset missing: %s", name, hookAssetPath)
		}

		if len(item.Bundles) == 0 && !item.Internal {
			return fmt.Errorf("preset %q must include at least one bundle", name)
		}
		for _, bundle := range item.Bundles {
			normalized, err := normalizeBundle(bundle)
			if err != nil {
				return fmt.Errorf("preset %q has invalid bundle: %w", name, err)
			}
			if !item.Internal {
				bundleCounts[normalized]++
			}
		}

		if len(item.OS) == 0 {
			return fmt.Errorf("preset %q must include at least one os target", name)
		}
		if strings.TrimSpace(item.PromptVersion) == "" {
			return fmt.Errorf("preset %q has empty prompt_version", name)
		}
		if strings.TrimSpace(item.HookVersion) == "" {
			return fmt.Errorf("preset %q has empty hook_version", name)
		}
	}

	if bundleCounts[BundleSecurity] != ExpectedSecurityCount {
		return fmt.Errorf("security bundle must contain %d presets, found %d", ExpectedSecurityCount, bundleCounts[BundleSecurity])
	}
	if bundleCounts[BundleQuality] != ExpectedQualityCount {
		return fmt.Errorf("quality bundle must contain %d presets, found %d", ExpectedQualityCount, bundleCounts[BundleQuality])
	}
	if len(internalPresets) != ExpectedInternalCount {
		return fmt.Errorf("catalog must contain exactly %d internal preset(s), found %d", ExpectedInternalCount, len(internalPresets))
	}

	return nil
}

// Find returns one preset by exact name.
func (catalog *Catalog) Find(name string) (CatalogItem, bool) {
	name = strings.TrimSpace(name)
	for _, item := range catalog.Presets {
		if item.Name == name {
			return item, true
		}
	}
	return CatalogItem{}, false
}

// ListVisible returns visible (non-internal) presets sorted by name, optionally filtered by bundle.
func (catalog *Catalog) ListVisible(bundle string) ([]CatalogItem, error) {
	normalized, err := normalizeBundle(bundle)
	if err != nil {
		return nil, err
	}

	filtered := make([]CatalogItem, 0, len(catalog.Presets))
	for _, item := range catalog.Presets {
		if item.Internal {
			continue
		}
		if normalized == "" {
			filtered = append(filtered, item)
			continue
		}
		for _, candidate := range item.Bundles {
			if strings.EqualFold(strings.TrimSpace(candidate), normalized) {
				filtered = append(filtered, item)
				break
			}
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})
	return filtered, nil
}

// List returns visible presets for backwards compatibility.
func (catalog *Catalog) List(bundle string) ([]CatalogItem, error) {
	return catalog.ListVisible(bundle)
}

// BundleMembers returns preset names in one bundle.
func (catalog *Catalog) BundleMembers(bundle string) ([]string, error) {
	items, err := catalog.ListVisible(bundle)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names, nil
}

// ReadHookContent reads one embedded hook asset for a preset.
func (catalog *Catalog) ReadHookContent(item CatalogItem) ([]byte, error) {
	path := filepath.ToSlash(filepath.Join("hooks", strings.TrimSpace(item.HookFile)))
	data, err := fs.ReadFile(embeddedAssets, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read preset hook asset %s: %w", path, err)
	}
	return data, nil
}

// BundleFor returns a deterministic bundle label for display.
func BundleFor(item CatalogItem) string {
	return strings.Join(sortedUnique(item.Bundles), ",")
}

// InternalAutoEnabled returns internal presets that must be auto-managed with factory presets.
func (catalog *Catalog) InternalAutoEnabled() []CatalogItem {
	items := make([]CatalogItem, 0, len(catalog.Presets))
	for _, item := range catalog.Presets {
		if item.Internal && item.AutoEnableWithFactory {
			items = append(items, item)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}
