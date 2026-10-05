//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// RemoveTool takes one executable off the sandbox PATH.
func (s *Sandbox) RemoveTool(name string) {
	s.t.Helper()
	if err := os.Remove(filepath.Join(s.Root, "tools", exe(name))); err != nil {
		s.t.Fatal(err)
	}
}

// ledgerPath is the sandbox's commit-review ledger shard.
func (s *Sandbox) ledgerPath() string {
	return filepath.Join(s.Home, ".steer", "state", "commit-review", "adjudication.e2e.jsonl")
}

// ledgerObjects reads every line of the ledger shard that parses as a JSON object.
func (s *Sandbox) ledgerObjects() []map[string]any {
	s.t.Helper()
	data, err := os.ReadFile(s.ledgerPath())
	if err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func skipAsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
}

// TestGuardsWithoutPython3DoNotBlock removes python3 after a guard is registered. The
// registered master cannot start, and a harness must not read that as a block.
//
// pins existing behaviour
func TestGuardsWithoutPython3DoNotBlock(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s := NewSandbox(t)
			s.MustSteer("bin", "install")
			s.RegisterNoVar(h.harness)
			command := s.RegisteredCommand(filepath.Join(s.Repo, h.config), "steer-hooks")
			s.RemoveTool("python3")
			payload, _ := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse", "tool_name": "Bash",
				"tool_input": map[string]any{"command": "var x = 1"},
				"session_id": "e2e-nopy", "cwd": s.Repo,
			})
			r := s.Run(s.Repo, string(payload), nil, "sh", "-c", command)
			if r.Code == 2 || strings.Contains(r.Stdout, `"deny"`) {
				t.Errorf("a master that cannot start blocks the tool call (exit %d):\n%s", r.Code, r.Output())
			}
		})
	}
}

// TestGateWithoutGitDoesNotBlock removes git after the gate is armed. No commit can
// happen, and the gate must neither refuse the call nor claim a review.
//
// pins existing behaviour
func TestGateWithoutGitDoesNotBlock(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s, g := newNativeGate(t, h.harness, h.config, h.sub)
			s.Script("finding")
			s.StageCode("mod.py")
			s.RemoveTool("git")
			reply := g.Hook("git -C "+s.Repo+" commit -m 'add mod'", s.Repo)
			if reply.Denied() {
				t.Errorf("the gate refused a commit it could not read:\n%s", reply.Raw)
			}
			if n := len(s.Calls()); n != 0 {
				t.Errorf("the gate made %d reviewer calls with no git", n)
			}
		})
	}
}

// TestGateOutsideARepositoryClaimsNoReview sends a commit for a directory that is not
// a repository through each harness's hook.
//
// pins existing behaviour
func TestGateOutsideARepositoryClaimsNoReview(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s, g := newNativeGate(t, h.harness, h.config, h.sub)
			s.Script("finding")
			plain := s.path("plain")
			if err := os.MkdirAll(plain, 0o755); err != nil {
				t.Fatal(err)
			}
			reply := g.Hook("git -C "+plain+" commit -m x", plain)
			if reply.Denied() || strings.Contains(reply.Text(), "found nothing") {
				t.Errorf("the gate refused or claimed a review outside a repository:\n%s", reply.Raw)
			}
			if n := len(s.Calls()); n != 0 {
				t.Errorf("the gate made %d reviewer calls outside a repository", n)
			}
		})
	}
}

// TestPrecommitInstallOutsideARepositoryRefusesInOneLine checks the pre-commit
// install from a directory that is not a repository.
//
// pins existing behaviour
func TestPrecommitInstallOutsideARepositoryRefusesInOneLine(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	plain := s.path("plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	r := s.Run(plain, "", nil, "steer", "commit-gate", "install", "--precommit")
	if r.Code == 0 {
		t.Errorf("install outside a repository exited 0:\n%s", r.Output())
	}
	if !strings.Contains(strings.ToLower(r.Output()), "repositor") {
		t.Errorf("the refusal does not say a repository is needed:\n%s", r.Output())
	}
}

// TestGateWithAnUnwritableStateDirProceeds makes the Steer state directory read-only.
// The review still runs and the commit proceeds; the reply says the row was lost.
//
// pins existing behaviour
func TestGateWithAnUnwritableStateDirProceeds(t *testing.T) {
	t.Parallel()
	skipAsRoot(t)
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s, g := newNativeGate(t, h.harness, h.config, h.sub)
			s.Script("clean")
			s.StageCode("mod.py")
			state := filepath.Join(s.Home, ".steer", "state")
			if err := os.MkdirAll(state, 0o755); err != nil {
				t.Fatal(err)
			}
			var dirs []string
			filepath.WalkDir(state, func(p string, d os.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					dirs = append(dirs, p)
				}
				return nil
			})
			for _, d := range dirs {
				if err := os.Chmod(d, 0o555); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				for _, d := range dirs {
					os.Chmod(d, 0o755)
				}
			})
			reply := g.Hook("git -C "+s.Repo+" commit -m 'add mod'", s.Repo)
			if reply.Denied() {
				t.Fatalf("the gate refused a commit because its state is read-only:\n%s", reply.Raw)
			}
			if !strings.Contains(reply.Text(), "could NOT be written") {
				t.Errorf("the reply does not say the ledger row was lost:\n%s", reply.Raw)
			}
		})
	}
}

// TestGateAfterATruncatedLedgerLine leaves the ledger shard ending in half a row, as a
// crash mid-append would. The next review must land a row that parses, and the
// readers must not fail on the shard.
//
// pins existing behaviour
func TestGateAfterATruncatedLedgerLine(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	if err := os.MkdirAll(filepath.Dir(s.ledgerPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ledgerPath(), []byte("not json at all\n{\"ts\":1,\"phase\":\"cle"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Script("clean")
	s.StageCode("mod.py")
	if reply := g.Hook("git -C "+s.Repo+" commit -m 'add mod'", s.Repo); reply.Denied() {
		t.Fatalf("the gate refused over a corrupt ledger:\n%s", reply.Raw)
	}
	var clean int
	for _, row := range s.ledgerObjects() {
		if row["phase"] == "clean" && row["repo"] == s.Repo {
			clean++
		}
	}
	if clean != 1 {
		data, _ := os.ReadFile(s.ledgerPath())
		t.Errorf("want the new clean row to parse, found %d:\n%s", clean, data)
	}
	for _, args := range [][]string{{"status", "--json"}, {"fix", "score", "--json"}, {"commit-gate", "status"}} {
		if r := s.Steer(args...); r.Code != 0 {
			t.Errorf("steer %v exited %d over a corrupt ledger:\n%s", args, r.Code, r.Output())
		}
	}
}

// TestConcurrentReviewsEachLandARow runs several sessions' reviews at once against
// one home. Every row must land whole.
//
// pins existing behaviour
func TestConcurrentReviewsEachLandARow(t *testing.T) {
	t.Parallel()
	const sessions = 6
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	s.Script("clean")
	s.StageCode("mod.py")
	var wg sync.WaitGroup
	replies := make([]HookReply, sessions)
	for i := range replies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			replies[i] = g.hookAs(fmt.Sprintf("e2e-concurrent-%d", i), "git -C "+s.Repo+" commit -m 'add mod'", s.Repo)
		}(i)
	}
	wg.Wait()
	for i, r := range replies {
		if r.Denied() {
			t.Errorf("session %d was refused:\n%s", i, r.Raw)
		}
	}
	if rows := s.LedgerRows(); len(rows) != sessions {
		t.Errorf("want %d ledger rows, got %d: %v", sessions, len(rows), rows)
	}
}

// TestFiringCapStopsBlocking keeps a finding standing over a changing diff. After the
// cap, the next firing reviews the fixes without refusing the commit.
//
// pins existing behaviour
func TestFiringCapStopsBlocking(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	s.Script("finding")
	s.StageCode("mod.py")
	cmd := "git -C " + s.Repo + " commit -m 'add mod'"
	for round := 1; round <= 4; round++ {
		reply := g.Hook(cmd, s.Repo)
		if !reply.Denied() {
			t.Fatalf("round %d: want a deny before the cap, got:\n%s", round, reply.Raw)
		}
		s.Write("mod.py", s.Git("show", ":mod.py")+fmt.Sprintf("FIX_%d = %d\n", round, round))
		s.Git("add", "mod.py")
	}
	reply := g.Hook(cmd, s.Repo)
	if reply.Denied() {
		t.Fatalf("the gate refused past its cap:\n%s", reply.Raw)
	}
	if !strings.Contains(reply.Text(), "reached its limit of 4 blocks") {
		t.Errorf("the capped reply does not name the cap:\n%s", reply.Raw)
	}
	if !strings.Contains(reply.Context, "CLAIM: stub finding 1.") {
		t.Errorf("the final review's findings do not reach the agent:\n%s", reply.Raw)
	}
}

// TestDenyNamesAnUncommittedAddendumEntry adds a project addendum entry without
// committing it. The gate reads entries from HEAD, so the deny must name it.
//
// pins existing behaviour
func TestDenyNamesAnUncommittedAddendumEntry(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	s.MustSteer("commit-gate", "addendum", "add", "no-style-nits", "--text", "naming preferences are not findings")
	s.Script("finding")
	s.StageCode("mod.py")
	reply := g.Hook("git -C "+s.Repo+" commit -m 'add mod'", s.Repo)
	if !reply.Denied() {
		t.Fatalf("want a deny, got:\n%s", reply.Raw)
	}
	if !strings.Contains(reply.Reason, "no-style-nits") {
		t.Errorf("the deny does not name the uncommitted entry:\n%s", reply.Reason)
	}
}

// TestRepositoryLinksAreRefused clones the shape of a hostile repository: each Steer
// write target in the checkout is a symlink to a file outside it. Each write must be
// refused with a message, and nothing may be written through the link.
//
// pins existing behaviour
func TestRepositoryLinksAreRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, link string
		dir        bool
		args       []string
	}{
		{"project store", ".steer/hooks", true, nil},
		{"claude settings", ".claude/settings.json", false, []string{"commit-gate", "install", "--harness", "claude-code"}},
		{"codex hooks", ".codex/hooks.json", false, []string{"commit-gate", "install", "--harness", "codex"}},
		{"archive", ".steer/hooks/archive", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewSandbox(t)
			s.Reviewer("claude-code", "sonnet", "", 3, 3)
			s.MustSteer("bin", "install")
			if tc.name == "archive" {
				s.RegisterNoVar("claude-code")
			}
			target := s.path("outside")
			if tc.dir {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(s.Repo, tc.link)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			mustLink(t, target, link)

			var r Result
			switch tc.name {
			case "project store":
				src := s.path(noVarHook + ".py")
				if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
					t.Fatal(err)
				}
				r = s.Steer("register", src, "--rule", noVarRule, "--harness", "claude-code")
			case "archive":
				r = s.Steer("remove", noVarHook, "--force", "--reason", "e2e")
			default:
				r = s.Steer(tc.args...)
			}
			if r.Code == 0 {
				t.Errorf("the write through %s exited 0:\n%s", tc.link, r.Output())
			}
			if !strings.Contains(strings.ToLower(r.Output()), "link") {
				t.Errorf("the refusal does not name the link:\n%s", r.Output())
			}
			if tc.dir {
				if entries, _ := os.ReadDir(target); len(entries) != 0 {
					t.Errorf("Steer wrote %d entries through the link", len(entries))
				}
			} else if data, _ := os.ReadFile(target); string(data) != "{}\n" {
				t.Errorf("Steer wrote through the link:\n%s", data)
			}
		})
	}
}
