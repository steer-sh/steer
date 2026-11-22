package status

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/protection"
)

// The reading says on, off by setting, or expected and missing; nothing is expected
// before a harness is set up, and only a guard expected and absent asks for a repair.
func TestProtectionReadsTheSettingAgainstTheStore(t *testing.T) {
	cases := []struct {
		enabled, setUp, running bool
		store                   string
		state                   string
		severity                string
	}{
		{true, true, true, protection.StoreCurrent, ProtectionOn, ""},
		{true, true, false, protection.StoreCurrent, ProtectionNotRunning, SeverityAttention},
		{true, false, false, protection.StoreCurrent, ProtectionNotSetUp, ""},
		{false, true, true, protection.StoreCurrent, ProtectionOff, SeverityInfo},
		{false, true, false, protection.StoreAbsent, ProtectionOff, SeverityInfo},
		{true, true, false, protection.StoreAbsent, ProtectionMissing, SeverityAttention},
		{true, true, true, protection.StoreForeign, ProtectionMissing, SeverityAttention},
		{true, true, true, protection.StoreOutdated, ProtectionOutdated, SeverityAttention},
		{true, false, false, protection.StoreAbsent, ProtectionNotSetUp, ""},
		{true, false, false, protection.StoreOutdated, ProtectionNotSetUp, ""},
	}
	for _, tc := range cases {
		state := protectionState(tc.enabled, tc.setUp, tc.running, tc.store)
		if state != tc.state {
			t.Errorf("enabled=%v setUp=%v store=%s: state %s, want %s", tc.enabled, tc.setUp, tc.store, state, tc.state)
		}
		issues := protectionIssues(ProtectionReading{State: state, Store: tc.store}, false)
		severity := ""
		if len(issues) > 0 {
			severity = issues[0].Severity
		}
		if severity != tc.severity {
			t.Errorf("state %s: issue severity %q, want %q", state, severity, tc.severity)
		}
		if severity == SeverityAttention && issues[0].Fix != ProtectionRepair {
			t.Errorf("state %s: fix %q", state, issues[0].Fix)
		}
	}
	legacy := protectionIssues(ProtectionReading{State: ProtectionOn, Legacy: []string{"global", "project"}}, false)
	if len(legacy) != 1 || legacy[0].Fix != "steer hooks rebuild --scope project" {
		t.Errorf("a repository's old copy reads %+v", legacy)
	}
	for _, reason := range []string{reasonUnapproved, reasonPiUnloaded} {
		unapproved := protectionIssues(ProtectionReading{State: ProtectionNotRunning, Reason: reason}, false)
		if len(unapproved) != 1 || unapproved[0].Fix == ProtectionRepair {
			t.Errorf("a runner the harness does not run (%s) names %+v", reason, unapproved)
		}
	}
	if kept := protectionIssues(ProtectionReading{State: ProtectionNotSetUp, Legacy: []string{"project"}}, false); len(kept) != 0 {
		t.Errorf("a repository's old copy is named for retirement with no global guard running: %+v", kept)
	}
}

// Pi runs the global guard only once a session loads its extension, so the reading
// is not-running until a load record names the extension's current bytes.
func TestProtectionWaitsForAPiSessionToLoadTheGuard(t *testing.T) {
	isolate(t)
	if _, err := protection.Reconcile(true, []harness.Registrar{pi.Registrar{}},
		protection.Options{Unverified: true}); err != nil {
		t.Fatal(err)
	}
	if r := ReadProtection(); r.State != ProtectionNotRunning || r.Reason != reasonPiUnloaded {
		t.Fatalf("before any Pi session: %+v", r)
	}

	bridge, err := pi.GuardBridgePath(claude.GlobalScope, ".")
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
	record := fmt.Sprintf(`{"path":%q,"digest":%q}`, resolved, hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(pi.LoadDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pi.LoadDir(), "guards.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := ReadProtection(); r.State != ProtectionOn {
		t.Fatalf("after a session loaded the current extension: %+v", r)
	}
}
