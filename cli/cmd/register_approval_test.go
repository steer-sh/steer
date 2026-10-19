package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
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
