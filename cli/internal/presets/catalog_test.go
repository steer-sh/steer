package presets

import (
	"strings"
	"testing"
)

func TestLoadCatalog(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	if got := len(catalog.Presets); got != ExpectedPresetCount {
		t.Fatalf("expected %d presets, got %d", ExpectedPresetCount, got)
	}

	names := map[string]struct{}{}
	bundleCounts := map[string]int{
		BundleSecurity: 0,
		BundleQuality:  0,
	}

	for _, item := range catalog.Presets {
		if _, exists := names[item.Name]; exists {
			t.Fatalf("duplicate preset name: %s", item.Name)
		}
		names[item.Name] = struct{}{}

		content, err := catalog.ReadHookContent(item)
		if err != nil {
			t.Fatalf("failed to read hook asset for %s: %v", item.Name, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			t.Fatalf("hook asset for %s is empty", item.Name)
		}

		for _, bundle := range item.Bundles {
			bundleCounts[bundle]++
		}
	}

	if bundleCounts[BundleSecurity] != ExpectedSecurityCount {
		t.Fatalf("expected %d security presets, got %d", ExpectedSecurityCount, bundleCounts[BundleSecurity])
	}
	if bundleCounts[BundleQuality] != ExpectedQualityCount {
		t.Fatalf("expected %d quality presets, got %d", ExpectedQualityCount, bundleCounts[BundleQuality])
	}
}

func TestCatalogBundleMembers(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	security, err := catalog.BundleMembers(BundleSecurity)
	if err != nil {
		t.Fatalf("BundleMembers(security) returned error: %v", err)
	}
	if len(security) != ExpectedSecurityCount {
		t.Fatalf("expected %d security members, got %d", ExpectedSecurityCount, len(security))
	}

	quality, err := catalog.BundleMembers(BundleQuality)
	if err != nil {
		t.Fatalf("BundleMembers(quality) returned error: %v", err)
	}
	if len(quality) != ExpectedQualityCount {
		t.Fatalf("expected %d quality members, got %d", ExpectedQualityCount, len(quality))
	}
}

func TestCatalogListRejectsInvalidBundle(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	if _, err := catalog.List("not-a-bundle"); err == nil {
		t.Fatalf("expected invalid bundle error, got nil")
	}
}
