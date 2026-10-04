//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmptyMachineReadings runs the first commands a new user meets on a machine with
// no Steer state, no reviewer CLI and no transcripts. Each one exits cleanly and states
// local absence; none names Pi, which is unlisted.
//
// pins existing behaviour
func TestEmptyMachineReadings(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithoutStubs())
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"status"}, []string{"Commits here are NOT reviewed.", "run: steer "}},
		{[]string{"commit-gate", "status"}, []string{"not armed"}},
		{[]string{"fix", "score", "--json"}, []string{"[]"}},
		{[]string{"fix", "list", "--json"}, []string{"[]"}},
		{[]string{"audit", "queue"}, []string{"No guard store on this machine holds an audit stream"}},
		{[]string{"check", "blocks"}, []string{"No Stop blocks recorded on this machine."}},
		{[]string{"archive", "list"}, []string{"no entries"}},
	}
	for _, tc := range cases {
		r := s.Steer(tc.args...)
		if r.Code != 0 {
			t.Errorf("steer %q exited %d:\n%s", tc.args, r.Code, r.Output())
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(r.Output(), want) {
				t.Errorf("steer %q lacks %q:\n%s", tc.args, want, r.Output())
			}
		}
		if strings.Contains(strings.ToLower(r.Output()), "pi:") {
			t.Errorf("steer %q names Pi on a machine with no Pi:\n%s", tc.args, r.Output())
		}
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".steer")); !os.IsNotExist(err) {
		t.Errorf("a read-only command created ~/.steer (%v)", err)
	}

	if r := s.Steer("report", "cadence"); r.Code != 0 || r.Stdout != "" {
		t.Errorf("report cadence exited %d and printed %q on an empty machine", r.Code, r.Stdout)
	}
	// cadence starts a detached refresh; it must finish before the home is removed.
	s.WaitForRefresh()
	if r := s.Steer("report", "show"); r.Code == 0 || !strings.Contains(r.Output(), "run `steer report` first") {
		t.Errorf("report show with no stored report exited %d:\n%s", r.Code, r.Output())
	}
	if r := s.Steer("dashboard"); r.Code == 0 || !strings.Contains(r.Output(), "steer status --json") {
		t.Errorf("dashboard without a terminal exited %d:\n%s", r.Code, r.Output())
	}
	r := s.Steer("report")
	if r.Code != 0 || !strings.Contains(r.Stderr, "claude-code: 0 file(s)") || strings.Contains(r.Stderr, "pi:") {
		t.Errorf("report over no transcripts exited %d:\n%s", r.Code, r.Stderr)
	}
}

// TestEmptyMachineStatusJSON checks the agent-facing snapshot on an empty machine:
// nothing reviews, the readiness answer names a repair, and every count is a zero
// over evidence this machine holds.
//
// pins existing behaviour
func TestEmptyMachineStatusJSON(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithoutStubs())
	r := s.MustSteer("status", "--json")
	var doc struct {
		Posture struct {
			Readiness struct {
				Reviewing bool `json:"reviewing"`
				Steps     []struct {
					Fix string `json:"fix"`
				} `json:"steps"`
			} `json:"readiness"`
			Plugins []any `json:"plugins"`
			Mods    []any `json:"mods"`
		} `json:"posture"`
		Telemetry struct {
			ReviewChains int `json:"review_chains"`
			AuditFirings int `json:"audit_firings"`
			CheckBlocks  int `json:"check_blocks"`
			Harnesses    []struct {
				Harness string `json:"harness"`
			} `json:"harnesses"`
		} `json:"telemetry"`
		Interventions []any `json:"interventions"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatalf("status --json: %v\n%s", err, r.Stdout)
	}
	p := doc.Posture
	if p.Readiness.Reviewing || len(p.Readiness.Steps) == 0 || !strings.HasPrefix(p.Readiness.Steps[0].Fix, "steer ") {
		t.Errorf("readiness = %+v, want not reviewing with a steer command as the first repair", p.Readiness)
	}
	if p.Plugins == nil || p.Mods == nil || doc.Interventions == nil {
		t.Errorf("an empty list was emitted as null: plugins %v, mods %v, interventions %v", p.Plugins, p.Mods, doc.Interventions)
	}
	tel := doc.Telemetry
	if tel.ReviewChains != 0 || tel.AuditFirings != 0 || tel.CheckBlocks != 0 {
		t.Errorf("telemetry on an empty machine = %+v", tel)
	}
	for _, h := range tel.Harnesses {
		if h.Harness == "pi" {
			t.Errorf("telemetry names Pi on a machine with no Pi evidence")
		}
	}
}
