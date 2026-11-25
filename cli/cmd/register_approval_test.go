package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/pi"
)

func TestRegisterNamesTheCodexApprovalUntilARecordExists(t *testing.T) {
	home := isolatedHome(t)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	targets := []harness.Registrar{codex.Registrar{}}

	if line := codexApprovalLine("global", targets); line != "" {
		t.Fatalf("no runner registered, got %q", line)
	}
	if _, err := codex.RegisterMaster("global", "."); err != nil {
		t.Fatal(err)
	}
	line := codexApprovalLine("global", targets)
	if !strings.Contains(line, "global-scope") || !strings.Contains(line, "/hooks") ||
		!strings.Contains(line, codex.MasterStatusMessage) {
		t.Fatalf("unapproved runner: got %q", line)
	}

	hooksFile, err := filepath.EvalSymlinks(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%s:pre_tool_use:0:0", hooksFile)
	record := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"x\"\n", key)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if line := codexApprovalLine("global", targets); line != "" {
		t.Fatalf("approval record present, got %q", line)
	}
}

// Pi runs a registered guard extension only once a session loads it, so register
// names /reload for Pi alone, and stops once a session loaded the extension.
func TestRegisterNamesPiReloadUntilASessionLoadsTheExtension(t *testing.T) {
	cases := []struct {
		name    string
		targets []harness.Registrar
		loaded  bool
		want    bool
	}{
		{"unloaded extension", []harness.Registrar{pi.Registrar{}}, false, true},
		{"pi not a target", []harness.Registrar{codex.Registrar{}}, false, false},
		{"current bytes loaded", []harness.Registrar{pi.Registrar{}}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
			t.Chdir(t.TempDir())
			if _, err := pi.RegisterMaster("global", "."); err != nil {
				t.Fatal(err)
			}
			if tc.loaded {
				bridge, err := pi.GuardBridgePath("global", ".")
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(bridge)
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := filepath.EvalSymlinks(bridge)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(content)
				record, err := json.Marshal(map[string]string{"path": resolved, "digest": hex.EncodeToString(sum[:])})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(pi.LoadDir(), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(pi.LoadDir(), "guards.json"), record, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			line := piReloadLine("global", tc.targets)
			if !tc.want {
				if line != "" {
					t.Fatalf("got %q, want none", line)
				}
				return
			}
			if !strings.HasPrefix(line, "Pi runs no global-scope guard") || !strings.Contains(line, "/reload") {
				t.Fatalf("got %q", line)
			}
		})
	}
}
