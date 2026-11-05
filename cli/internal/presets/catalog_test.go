package presets

import (
	"strings"
	"testing"
)

func TestLoadCatalogPartitionsVisibleAndInternal(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	var visibleCount int
	var internalCount int
	bundleCounts := map[string]int{
		BundleSecurity: 0,
		BundleQuality:  0,
	}
	selfProtectionFound := false

	for _, item := range catalog.Presets {
		content, err := catalog.ReadHookContent(item)
		if err != nil {
			t.Fatalf("failed to read hook asset for %s: %v", item.Name, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			t.Fatalf("hook asset for %s is empty", item.Name)
		}

		if item.Internal {
			internalCount++
			if item.Name == "steer-self-protection" {
				selfProtectionFound = true
				if !item.AutoEnableWithFactory {
					t.Fatalf("steer-self-protection must be auto_enable_with_factory")
				}
			}
			continue
		}
		visibleCount++

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
	if internalCount != ExpectedInternalCount {
		t.Fatalf("expected %d internal presets, got %d", ExpectedInternalCount, internalCount)
	}
	if visibleCount != ExpectedSecurityCount+ExpectedQualityCount {
		t.Fatalf("expected %d visible presets, got %d", ExpectedSecurityCount+ExpectedQualityCount, visibleCount)
	}
	if !selfProtectionFound {
		t.Fatalf("expected internal preset steer-self-protection to exist")
	}
}

func TestCatalogBundleMembersVisibleOnly(t *testing.T) {
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

func TestCatalogListVisibleRejectsInvalidBundle(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	if _, err := catalog.ListVisible("not-a-bundle"); err == nil {
		t.Fatalf("expected invalid bundle error, got nil")
	}
}

func TestInternalPresetCanBeBundleless(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	item, ok := catalog.Find("steer-self-protection")
	if !ok {
		t.Fatalf("expected steer-self-protection in catalog")
	}
	if !item.Internal {
		t.Fatalf("steer-self-protection must be internal")
	}
	if len(item.Bundles) != 0 {
		t.Fatalf("expected internal preset to allow empty bundles, got %v", item.Bundles)
	}
}

func TestValidateCatalogEnforcesInternalCount(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	filtered := make([]CatalogItem, 0, len(catalog.Presets))
	for _, item := range catalog.Presets {
		if item.Internal {
			continue
		}
		filtered = append(filtered, item)
	}

	missingInternal := &Catalog{
		CatalogVersion: catalog.CatalogVersion,
		Presets:        filtered,
	}

	err = validateCatalog(missingInternal)
	if err == nil {
		t.Fatalf("expected internal count validation error, got nil")
	}
	if !strings.Contains(err.Error(), "internal preset") {
		t.Fatalf("expected internal preset error, got %v", err)
	}
}
