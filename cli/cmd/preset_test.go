package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/presets"
)

// The self-protection block names `steer preset disable steer-self-protection` as the
// user's removal command, so that command must remove the internal preset.
func TestPresetDisableRemovesTheInternalPresetItsBlockNames(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	_, visible := loadCatalogAndOneVisiblePreset(t)
	if _, err := presets.EnablePreset(claude.ProjectScope, catalog, visible, false); err != nil {
		t.Fatal(err)
	}
	if _, err := presets.EnsureAutoManagedFactoryPresets(claude.ProjectScope, catalog, false); err != nil {
		t.Fatal(err)
	}
	internal, _ := catalog.Find("steer-self-protection")
	hook := internal.HookFile[:len(internal.HookFile)-len(".py")]
	if manifest, err := claude.LoadManifest(claude.ProjectScope); err != nil || manifest.Hooks[hook].Source == "" {
		t.Fatalf("setup did not install %s (%v)", hook, err)
	}

	if err := presetDisableCmd.RunE(presetDisableCmd, []string{"steer-self-protection"}); err != nil {
		t.Fatalf("disable steer-self-protection: %v", err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Hooks[hook]; ok {
		t.Fatalf("%s is still in the manifest after disable", hook)
	}
}

// The NAME column fits the longest catalog name, so STATUS starts at one
// offset on the header and on every row.
func TestPresetListAlignsEveryRowToTheLongestName(t *testing.T) {
	fakeHomes(t)
	out := captureStdout(t, func() {
		if err := presetListCmd.RunE(presetListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	column := -1
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "NAME ") {
			column = strings.Index(line, "STATUS")
		}
	}
	if column < 0 {
		t.Fatalf("no header row:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		for _, status := range []string{" disabled ", " enabled "} {
			if i := strings.Index(line, status); i >= 0 && i+1 != column {
				t.Errorf("STATUS at %d, header at %d: %q", i+1, column, line)
			}
		}
	}
}

// An enable that also installs the internal self-protection preset names it, since
// that preset adds a guard and an instruction rule the user did not ask for.
func TestPresetEnableNamesTheInternalPresetItAlsoInstalls(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	out := captureStdout(t, func() {
		if err := presetEnableCmd.RunE(presetEnableCmd, []string{"sql-injection-prevention"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Also installed: steer-self-protection") || strings.Contains(out, "auto-managed") {
		t.Errorf("the enable did not name the internal preset it installed:\n%s", out)
	}
}
