package checks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Init writes an empty manifest into the repository's store, and a second Init
// keeps the declarations already there.
func TestInitWritesAnEmptyManifestAndKeepsAnExistingOne(t *testing.T) {
	root := t.TempDir()
	m, created, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !created || m.Version != ManifestVersion || len(m.Checks) != 0 {
		t.Fatalf("Init = %+v created=%v; want an empty version-%d manifest", m, created, ManifestVersion)
	}
	if _, err := os.Stat(filepath.Join(root, ".steer", "checks", "manifest.json")); err != nil {
		t.Fatalf("manifest not at .steer/checks/manifest.json: %v", err)
	}

	m.Checks = []Declaration{{ID: "no-count", Kind: KindScript, Script: ".steer/checks/no-count.sh"}}
	if err := Save(root, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, created, err := Init(root)
	if err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if created || len(again.Checks) != 1 || again.Checks[0].ID != "no-count" {
		t.Fatalf("second Init = %+v created=%v; want the saved check kept", again, created)
	}
}

// A repository without a manifest is reported as such, not as an empty manifest.
func TestLoadReportsAMissingManifest(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("Load on an empty repository: err = %v; want ErrNoManifest", err)
	}
}

// A declaration names one binding kind with its own fields, an id usable as a
// file name, and paths that stay inside the repository.
func TestADeclarationNamesOneBindingInsideTheRepository(t *testing.T) {
	valid := []Declaration{
		{ID: "no-count", Kind: KindScript, Script: "scripts/no-count.sh", Inputs: []string{"scripts/lib.sh"}},
		{ID: "json_contract", Kind: KindGoTest, Package: "cli/internal/status", Test: "TestJSONContractIsStable"},
		{ID: "root-test", Kind: KindGoTest, Package: ".", Test: "Test"},
	}
	for _, d := range valid {
		if err := d.Validate(); err != nil {
			t.Errorf("%+v refused: %v", d, err)
		}
	}

	invalid := map[string]Declaration{
		"empty id":          {Kind: KindScript, Script: "a.sh"},
		"id with a slash":   {ID: "a/b", Kind: KindScript, Script: "a.sh"},
		"unknown kind":      {ID: "a", Kind: "python", Script: "a.sh"},
		"script missing":    {ID: "a", Kind: KindScript},
		"script with test":  {ID: "a", Kind: KindScript, Script: "a.sh", Test: "TestA"},
		"absolute script":   {ID: "a", Kind: KindScript, Script: "/tmp/a.sh"},
		"escaping script":   {ID: "a", Kind: KindScript, Script: "../a.sh"},
		"unclean script":    {ID: "a", Kind: KindScript, Script: "scripts/../a.sh"},
		"backslash script":  {ID: "a", Kind: KindScript, Script: `scripts\a.sh`},
		"go-test no test":   {ID: "a", Kind: KindGoTest, Package: "pkg"},
		"go-test script":    {ID: "a", Kind: KindGoTest, Package: "pkg", Test: "TestA", Script: "a.sh"},
		"lowercase test":    {ID: "a", Kind: KindGoTest, Package: "pkg", Test: "Testable"},
		"subtest":           {ID: "a", Kind: KindGoTest, Package: "pkg", Test: "TestA/case"},
		"escaping package":  {ID: "a", Kind: KindGoTest, Package: "../pkg", Test: "TestA"},
		"escaping input":    {ID: "a", Kind: KindScript, Script: "a.sh", Inputs: []string{"../secret"}},
		"absolute input":    {ID: "a", Kind: KindScript, Script: "a.sh", Inputs: []string{"/etc/passwd"}},
		"empty input":       {ID: "a", Kind: KindScript, Script: "a.sh", Inputs: []string{""}},
		"benchmark as test": {ID: "a", Kind: KindGoTest, Package: "pkg", Test: "BenchmarkA"},
	}
	for name, d := range invalid {
		if err := d.Validate(); err == nil {
			t.Errorf("%s: %+v accepted", name, d)
		}
	}
}

// A manifest from another format version, or one declaring an id twice, is
// refused at load and at save.
func TestAManifestRefusesAnotherVersionAndADuplicateID(t *testing.T) {
	root := t.TempDir()
	script := Declaration{ID: "a", Kind: KindScript, Script: "a.sh"}
	for name, m := range map[string]Manifest{
		"future version": {Version: ManifestVersion + 1},
		"duplicate id":   {Version: ManifestVersion, Checks: []Declaration{script, script}},
		"invalid check":  {Version: ManifestVersion, Checks: []Declaration{{ID: "a", Kind: KindScript}}},
	} {
		if err := Save(root, m); err == nil {
			t.Errorf("%s: Save accepted it", name)
		}
		if _, err := os.Stat(ManifestPath(root)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a refused Save wrote a manifest", name)
		}
	}

	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(root), []byte(`{"version": 2, "checks": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Load of a version-2 manifest: err = %v; want a version refusal", err)
	}
}

// Save writes checks in id order, so an added check does not reorder a diff.
func TestSaveWritesChecksInIDOrder(t *testing.T) {
	root := t.TempDir()
	m := Manifest{Version: ManifestVersion, Checks: []Declaration{
		{ID: "zeta", Kind: KindScript, Script: "z.sh"},
		{ID: "alpha", Kind: KindScript, Script: "a.sh"},
	}}
	if err := Save(root, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Checks) != 2 || got.Checks[0].ID != "alpha" || got.Checks[1].ID != "zeta" {
		t.Fatalf("Load = %+v; want alpha before zeta", got.Checks)
	}
}

// The guide covers both binding kinds, the exclusions and the defect case.
func TestTheGuideCoversBindingsExclusionsAndDefectCases(t *testing.T) {
	g := Guide()
	for _, want := range []string{"`script`", "`go-test`", "network", "flake", "defect case", "300-second", "pipefail", SnapshotRefPrefix} {
		if !strings.Contains(g, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
}
