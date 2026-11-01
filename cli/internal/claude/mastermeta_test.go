package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// master is a generated master's shape: a stamp line, imports, the build
// digest, the meta dict and the sources dict.
const master = `#!/usr/bin/env python3
# Steer-Master: version=0.1.0
import json
import sys

STEER_MASTER_BUILD_DIGEST = "9905e989dce5c09a"

STEER_HOOK_META = {
    "first-hook": {
        "hook_digest": "0ecadfd7dbf51317",
        "hook_name": "first-hook",
        "rule": "a rule with a { brace and a \" quote",
        "rule_hash": "06a6e0b8884224eb",
        "source": "manual"
    },
    "second-hook": {
        "hook_digest": "c7ab764c49424c7e",
        "hook_name": "second-hook",
        "rule_hash": "a2a7542310f0334b"
    }
}

STEER_HOOK_SOURCES = {
    "first-hook": "print('}')"
}

def main():
    pass
`

func writeMaster(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MasterHookName), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadMasterIdentityTakesEachGuardsStampsFromTheMaster(t *testing.T) {
	identity, err := ReadMasterIdentity(writeMaster(t, master))
	if err != nil {
		t.Fatal(err)
	}
	if identity.BuildDigest != "9905e989dce5c09a" {
		t.Errorf("build digest = %q; want the master's own", identity.BuildDigest)
	}
	if len(identity.Hooks) != 2 {
		t.Fatalf("hooks = %+v; want both", identity.Hooks)
	}
	first := identity.Hooks["first-hook"]
	if first.HookDigest != "0ecadfd7dbf51317" || first.RuleHash != "06a6e0b8884224eb" {
		t.Errorf("first hook = %+v; want its two stamps", first)
	}
	// A brace inside a rule string must not end the dict, and the sources dict
	// after it must not be read into the meta.
	if !strings.Contains(first.Rule, "{ brace") {
		t.Errorf("rule = %q; want the braced rule whole", first.Rule)
	}
	if _, ok := identity.Hooks["second-hook"]; !ok {
		t.Error("the dict ended before the second hook")
	}
}

func TestReadMasterIdentitySeparatesNoMasterFromABadOne(t *testing.T) {
	// A store that installed no guard is not a failure to read one.
	empty, err := ReadMasterIdentity(t.TempDir())
	if err != nil {
		t.Fatalf("an absent master returned an error: %v", err)
	}
	if len(empty.Hooks) != 0 || empty.BuildDigest != "" {
		t.Errorf("an absent master yielded %+v; want nothing", empty)
	}

	if _, err := ReadMasterIdentity(""); err != nil {
		t.Errorf("an unresolved store returned an error: %v", err)
	}

	// A master this reader cannot understand is an error, never silence: the
	// installed identity would otherwise read as absent.
	if _, err := ReadMasterIdentity(writeMaster(t, "import sys\n")); err == nil {
		t.Error("a master declaring no meta was read as empty")
	}
	if _, err := ReadMasterIdentity(writeMaster(t,
		"STEER_HOOK_META = {\"a\": {\n")); err == nil {
		t.Error("an unterminated meta dict was read as empty")
	}
}
