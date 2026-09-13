package claude

import (
	"strings"
	"testing"
)

func TestAnAddendumEntryRoundTripsThroughTheStore(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallAddendum(ProjectScope, "no-style", "  style is not a finding  "); err != nil {
		t.Fatal(err)
	}
	entries, err := ListAddendum(ProjectScope)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %#v, err = %v", entries, err)
	}
	if entries[0].Text != "style is not a finding" || entries[0].TextHash != HashRule("style is not a finding") {
		t.Fatalf("entry = %#v", entries[0])
	}

	text, err := RemoveAddendum(ProjectScope, "no-style")
	if err != nil || text != "style is not a finding" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
	if entries, _ := ListAddendum(ProjectScope); len(entries) != 0 {
		t.Fatalf("removal left %#v", entries)
	}
}

// A guard install re-saves the typed manifest, so the addendum key must survive it.
func TestAGuardInstallKeepsTheAddendum(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallAddendum(ProjectScope, "no-style", "style is not a finding"); err != nil {
		t.Fatal(err)
	}
	installTestHook(t, "no-bare-python", "use python3 instead")
	if entries, _ := ListAddendum(ProjectScope); len(entries) != 1 {
		t.Fatalf("guard install dropped the addendum: %#v", entries)
	}
}

func TestAnAddendumIDIsUniqueAcrossTheStore(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")
	if _, err := InstallManagedRule(ProjectScope, "config-path", "the rule"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"no-bare-python", "config-path"} {
		if _, err := InstallAddendum(ProjectScope, id, "text "+id); err == nil ||
			!strings.Contains(err.Error(), "already names") {
			t.Fatalf("%s: err = %v", id, err)
		}
	}

	if _, err := InstallAddendum(ProjectScope, "no-style", "style is not a finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallManagedRule(ProjectScope, "no-style", "prose"); err == nil ||
		!strings.Contains(err.Error(), "review addendum") {
		t.Fatalf("rule over addendum id: err = %v", err)
	}
	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "no-style",
		Rule:     "the guard rule",
		Content:  []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n"),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil || !strings.Contains(err.Error(), "review addendum") {
		t.Fatalf("guard over addendum id: err = %v", err)
	}
}

func TestADuplicateAddendumUnderAnotherIDIsRefused(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallAddendum(ProjectScope, "first", "style is not a finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAddendum(ProjectScope, "second", "style  is not a finding"); err == nil ||
		!strings.Contains(err.Error(), "already holds this addendum entry") {
		t.Fatalf("err = %v", err)
	}
}
