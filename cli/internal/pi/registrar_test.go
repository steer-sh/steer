package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/version"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-home"))
	return home
}

func TestRegistrarRoundTripBakesTheRegisteredMasterPath(t *testing.T) {
	home := isolateHome(t)
	oldVersion := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = oldVersion })

	path, err := RegisterMaster(harness.GlobalScope, ".")
	if err != nil {
		t.Fatalf("RegisterMaster: %v", err)
	}
	wantBridge := filepath.Join(home, "pi-home", "extensions", guardBridgeName)
	if path != wantBridge {
		t.Fatalf("bridge path = %q, want %q", path, wantBridge)
	}

	master, err := (Registrar{}).RegisteredMasterPath(harness.GlobalScope)
	if err != nil {
		t.Fatalf("RegisteredMasterPath: %v", err)
	}
	wantMaster := filepath.Join(home, ".steer", "hooks", "steer-hooks.py")
	if master != wantMaster {
		t.Fatalf("registered master = %q, want %q", master, wantMaster)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		bridgeStamp + `{"bridge":"` + GuardBridgeDigest() + `","version":"1.2.3","channel":"guards"}`,
		`[STEER_MASTER, "--harness", "pi"]`,
		`case "powershell":`,
		`tool_name: "MultiEdit"`,
		`typeof input.oldText === "string"`,
		`{ cwd: payload.cwd, encoding: "utf8"`,
		`session_id: ctx.sessionManager.getSessionId()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated bridge is missing %q", want)
		}
	}

	if _, err := RemoveMaster(harness.GlobalScope, "."); err != nil {
		t.Fatalf("RemoveMaster: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("bridge still exists after removal: %v", err)
	}
}

// A project bridge names the master relative to the repository root, so a bridge
// committed in one checkout runs the master of whichever checkout Pi opens it in,
// never the checkout that wrote it.
func TestProjectRegistrarNamesTheMasterRelativeToTheRepository(t *testing.T) {
	isolateHome(t)
	// The target decides both the bridge's directory and the store's repository,
	// so a registration written for one checkout never names another.
	target := t.TempDir()
	path, err := RegisterMaster(harness.ProjectScope, target)
	if err != nil {
		t.Fatalf("RegisterMaster: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := masterFromBridge(content)
	if got != ".steer/hooks/steer-hooks.py" {
		t.Fatalf("registered master = %q, want the repository-relative path", got)
	}
	if strings.Contains(string(content), target) {
		t.Fatalf("the bridge names the checkout that wrote it:\n%s", content)
	}
	gate, err := RegisterCommitGateAndResolver(harness.ProjectScope, target,
		"steer commit-gate review-pi", 360, "steer commit-gate backfill", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveMaster(harness.ProjectScope, target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.ProjectScope, target); err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []string{path, gate} {
		if _, err := os.Stat(bridge); !os.IsNotExist(err) {
			t.Fatalf("ordinary project bridge was not removed: %v", err)
		}
	}
}

func TestRegistrarRefusesAnUnstampedExtension(t *testing.T) {
	isolateHome(t)
	dir, err := ExtensionsDir(harness.GlobalScope, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, guardBridgeName)
	const foreign = "export default function () {}\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterMaster(harness.GlobalScope, "."); err == nil {
		t.Fatal("RegisterMaster replaced an unstamped extension")
	}
	if _, err := RemoveMaster(harness.GlobalScope, "."); err == nil {
		t.Fatal("RemoveMaster removed an unstamped extension")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != foreign {
		t.Fatalf("foreign extension changed: content=%q err=%v", content, err)
	}
}

func TestInstructionFileUsesThePiConfigDirectoryOnlyAtGlobalScope(t *testing.T) {
	home := isolateHome(t)
	project, err := InstructionFile(harness.ProjectScope, "repo")
	if err != nil || project != filepath.Join("repo", "AGENTS.md") {
		t.Fatalf("project instruction = %q, %v", project, err)
	}
	global, err := InstructionFile(harness.GlobalScope, "repo")
	if err != nil || global != filepath.Join(home, "pi-home", "AGENTS.md") {
		t.Fatalf("global instruction = %q, %v", global, err)
	}
}

func TestGuardBridgeDigestTracksTheEmbeddedSource(t *testing.T) {
	digest := GuardBridgeDigest()
	if len(digest) != 8 {
		t.Fatalf("digest = %q, want 8 hex characters", digest)
	}
	if digest != GuardBridgeDigest() {
		t.Fatal("digest is not deterministic")
	}
}
