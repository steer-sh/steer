package cmd

import (
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
