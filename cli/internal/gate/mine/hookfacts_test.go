package mine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStore places a manifest and its guard file in one scope's neutral store.
func writeStore(t *testing.T, dir, hookID, rule, version, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{"` + hookID + `":{"file":"` + hookID + `.py","rule":"` + rule +
		`","hook_version":"` + version + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hookID+".py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSteerHookFactsReadsTheNeutralStoreAndTheImplementationIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "no-bare-python", "use python3",
		"factory-hook-v2", "print('v2')\n")

	facts, err := SteerHookFacts("no-bare-python", "")
	if err != nil {
		t.Fatal(err)
	}
	if facts["rule"] != "use python3" || facts["hook_scope"] != "global" ||
		facts["hook_version"] != "factory-hook-v2" || facts["hook_file"] != "no-bare-python.py" {
		t.Fatalf("facts = %#v", facts)
	}
	digest, _ := facts["hook_digest"].(string)
	if len(digest) != 16 {
		t.Fatalf("hook_digest = %q", digest)
	}

	// The digest names the code, so a repair with an unchanged rule moves it alone.
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "no-bare-python", "use python3",
		"factory-hook-v2", "print('repaired')\n")
	repaired, err := SteerHookFacts("no-bare-python", "")
	if err != nil {
		t.Fatal(err)
	}
	if repaired["rule_hash"] != facts["rule_hash"] {
		t.Fatalf("rule hash moved: %v -> %v", facts["rule_hash"], repaired["rule_hash"])
	}
	if repaired["hook_digest"] == facts["hook_digest"] {
		t.Fatalf("hook_digest did not move: %v", repaired["hook_digest"])
	}
}

func TestSteerHookFactsRefusesAnIDHeldByBothStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	t.Chdir(project)
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "shared-id", "global rule", "v1", "g")
	writeStore(t, filepath.Join(project, ".steer", "hooks"), "shared-id", "project rule", "v1", "p")

	if _, err := SteerHookFacts("shared-id", ""); err == nil ||
		!strings.Contains(err.Error(), "--hook-scope") {
		t.Fatalf("err = %v", err)
	}
	facts, err := SteerHookFacts("shared-id", "project")
	if err != nil || facts["rule"] != "project rule" {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
}

func TestSteerHookFactsReportsAnAbsentIDAsNoFacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	facts, err := SteerHookFacts("absent", "")
	if facts != nil || err != nil {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
	if _, err := SteerHookFacts("absent", "everywhere"); err == nil {
		t.Fatal("an invalid scope was accepted")
	}
}

func TestSteerHookFactsReportsACorruptManifestRatherThanAnAbsentHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".steer", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SteerHookFacts("any-hook", ""); err == nil ||
		!strings.Contains(err.Error(), "guard manifest") {
		t.Fatalf("err = %v", err)
	}
}
