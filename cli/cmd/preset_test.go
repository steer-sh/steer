package cmd

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/presets"
)

// The self-protection guard skips a flag's value by name, so its table must hold
// every value-taking flag of the commands it reads, and only those.
func TestSelfProtectionValueFlagTableMatchesTheCLI(t *testing.T) {
	want := map[string]bool{}
	collect := func(flag *pflag.Flag) {
		if flag.NoOptDefVal != "" {
			return
		}
		want["--"+flag.Name] = true
		if flag.Shorthand != "" {
			want["-"+flag.Shorthand] = true
		}
	}
	rootCmd.PersistentFlags().VisitAll(collect)
	commands := []*cobra.Command{removeCmd, registerCmd, presetCmd}
	commands = append(commands, presetCmd.Commands()...)
	for _, command := range commands {
		command.PersistentFlags().VisitAll(collect)
		command.Flags().VisitAll(collect)
	}

	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	item, ok := catalog.Find("steer-self-protection")
	if !ok {
		t.Fatal("steer-self-protection missing from catalog")
	}
	source, err := catalog.ReadHookContent(item)
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?s)STEER_VALUE_FLAGS = \{(.*?)\n\}`).FindSubmatch(source)
	if table == nil {
		t.Fatal("STEER_VALUE_FLAGS missing from the guard source")
	}
	var got []string
	for _, match := range regexp.MustCompile(`"(-[^"]+)"`).FindAllSubmatch(table[1], -1) {
		got = append(got, string(match[1]))
	}
	sort.Strings(got)

	var expected []string
	for flag := range want {
		expected = append(expected, flag)
	}
	sort.Strings(expected)
	if !slices.Equal(got, expected) {
		t.Fatalf("guard table = %v, CLI value-taking flags = %v", got, expected)
	}
}

// The self-protection block names `steer preset disable steer-self-protection` as the
// user's removal command, so that command must remove the internal preset.
func TestPresetDisableRemovesTheInternalPresetItsBlockNames(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	catalog, visible := loadCatalogAndOneVisiblePreset(t)
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

// With no optional preset in the catalog, list says so rather than printing an
// empty table.
func TestPresetListSaysNoOptionalPresetShips(t *testing.T) {
	fakeHomes(t)
	out := captureStdout(t, func() {
		if err := presetListCmd.RunE(presetListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "ships no optional presets") || !strings.Contains(out, "--prune-orphans") {
		t.Fatalf("list does not say no optional preset ships:\n%s", out)
	}

	old := scope
	scope = claude.GlobalScope
	t.Cleanup(func() { scope = old })
	out = captureStdout(t, func() {
		if err := presetListCmd.RunE(presetListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "--prune-orphans --scope global") {
		t.Fatalf("a global list names commands for the project scope:\n%s", out)
	}

	presetListBundle = "typo"
	t.Cleanup(func() { presetListBundle = "" })
	if err := presetListCmd.RunE(presetListCmd, nil); err == nil {
		t.Fatal("list accepted an invalid bundle")
	}
}

// A guard's block names `steer preset disable <preset>` as its removal, so that
// command removes a guard whose preset this Steer no longer ships, and the
// self-protection that stood beside it alone.
func TestPresetDisableRemovesAGuardWhosePresetLeftTheCatalog(t *testing.T) {
	fakeHomes(t)
	old := scope
	scope = claude.ProjectScope
	t.Cleanup(func() { scope = old })
	_, err := claude.InstallManagedHook(claude.ProjectScope, claude.InstallManagedHookInput{
		HookName: "sql-injection-prevention",
		Rule:     "sql safety",
		Content:  []byte("#!/usr/bin/env python3\n\ndef main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceFactory, SourceID: "sql-injection-prevention"},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := presets.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := presets.EnsureAutoManagedFactoryPresets(claude.ProjectScope, catalog, false); err != nil {
		t.Fatal(err)
	}

	if err := presetDisableCmd.RunE(presetDisableCmd, []string{"sql-injection-prevention"}); err != nil {
		t.Fatalf("disable of a removed preset: %v", err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Hooks) != 0 {
		t.Fatalf("guards left after disable: %v", manifest.Hooks)
	}
}
