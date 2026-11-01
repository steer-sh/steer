package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

func TestCommitGateBridgeRoundTrip(t *testing.T) {
	home := isolateHome(t)
	path, err := RegisterCommitGateAndResolver(harness.GlobalScope, ".",
		"/opt/steer commit-gate review-pi", 360,
		"/opt/steer commit-gate backfill --if-due --apply --quiet", 30)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "pi-home", "extensions", gateBridgeName)
	if path != want {
		t.Fatalf("bridge path = %q, want %q", path, want)
	}
	gate := CommitGateArming(harness.GlobalScope, ".")
	if !gate.Registered || gate.Timeout != 360 || gate.Command != "/opt/steer commit-gate review-pi" {
		t.Fatalf("gate arming = %#v", gate)
	}
	resolver := ResolverArming(harness.GlobalScope, ".")
	if !resolver.Registered || resolver.Timeout != 30 ||
		resolver.Command != "/opt/steer commit-gate backfill --if-due --apply --quiet" {
		t.Fatalf("resolver arming = %#v", resolver)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"channel":"commit-gate"`, `pi.on("tool_call"`, `pi.on("turn_end"`,
		`session_id: ctx.sessionManager.getSessionId()`, `detached: true`,
		`pi.sendMessage(`, `{ deliverAs: "steer" }`, `return { block: true, reason: reply.reason }`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("generated bridge is missing %q", want)
		}
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("gate bridge still exists after removal: %v", err)
	}
}

func TestGateAndGuardBridgesAreIndependent(t *testing.T) {
	isolateHome(t)
	guardPath, err := RegisterMaster(harness.GlobalScope, ".")
	if err != nil {
		t.Fatal(err)
	}
	gatePath, err := RegisterCommitGateAndResolver(harness.GlobalScope, ".",
		"steer commit-gate review-pi", 360, "steer commit-gate backfill", 30)
	if err != nil {
		t.Fatal(err)
	}
	if guardPath == gatePath {
		t.Fatal("both channels registered into one file")
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(guardPath); err != nil {
		t.Fatalf("removing the gate removed the guard bridge: %v", err)
	}
}

func TestGateRemovalRefusesAnUnstampedExtension(t *testing.T) {
	isolateHome(t)
	dir, err := ExtensionsDir(harness.GlobalScope, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, gateBridgeName)
	if err := os.WriteFile(path, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err == nil {
		t.Fatal("removed an unstamped Pi extension")
	}
}
