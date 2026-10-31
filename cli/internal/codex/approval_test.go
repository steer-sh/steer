package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The hashes are ones Codex CLI 0.166.0 saved for these definitions in a real
// config.toml, so a change to the normalization fails here first.
func TestHandlerHashMatchesCodexSavedHashes(t *testing.T) {
	limit := float64(0)
	for _, tc := range []struct {
		name string
		slot handlerSlot
		want string
	}{
		{"timeout, status and context limit", handlerSlot{event: "PreToolUse", matcher: "Bash", hook: map[string]any{
			"type": "command", "command": "/Users/jeremy/.steer/bin/steer commit-gate review-codex",
			"statusMessage": "red-teaming the commit ...", "timeout": float64(460), "additionalContextLimit": limit,
		}}, "sha256:ced9ff1a7ebe13dcde1e5815434d86f91856ec16079ab560ce27c4482ca5f190"},
		{"default timeout", handlerSlot{event: "PreToolUse", matcher: "Bash|apply_patch", hook: map[string]any{
			"type": "command", "command": "python3 /Users/jeremy/.steer/hooks/steer-hooks.py --harness codex",
			"statusMessage": "checking the change against your rules ...",
		}}, "sha256:1397884f71e58f3ffa8ef757db744914a4e183968c33e43703ca08077bfaa8c1"},
		{"no matcher on Stop", handlerSlot{event: "Stop", hook: map[string]any{
			"type": "command", "command": "/Users/jeremy/.steer/bin/steer commit-gate backfill --if-due --apply --quiet",
			"timeout": float64(30),
		}}, "sha256:6acfac2f80caa7fff4407002694ae58c74e611ac17e90c05d0d209dd3fa3c006"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := handlerHash(tc.slot)
			if !ok || got != tc.want {
				t.Errorf("hash = %s, %v; want %s", got, ok, tc.want)
			}
		})
	}
}

func TestHandlerHashRefusesAShapeItDoesNotCover(t *testing.T) {
	for name, hook := range map[string]map[string]any{
		"mcp":   {"type": "mcp_tool", "server": "s", "tool": "t"},
		"async": {"type": "command", "command": "x", "async": true},
	} {
		if _, ok := handlerHash(handlerSlot{event: "Stop", hook: hook}); ok {
			t.Errorf("%s: hash computed for an uncovered shape", name)
		}
	}
}

func TestApprovalOfReadsEachState(t *testing.T) {
	hook := map[string]any{"type": "command", "command": "steer check hook", "timeout": float64(30)}
	slot := handlerSlot{event: "Stop", hook: hook, keys: []string{"/h/hooks.json:stop:0:0"}}
	hash, ok := handlerHash(slot)
	if !ok {
		t.Fatal("no hash")
	}
	header := `[hooks.state."/h/hooks.json:stop:0:0"]`
	for _, tc := range []struct{ name, config, want string }{
		{"no section", "", ApprovalMissing},
		{"approved", fmt.Sprintf("%s\ntrusted_hash = %q\n", header, hash), ApprovalApproved},
		{"changed", fmt.Sprintf("%s\ntrusted_hash = %q\n", header, "sha256:other"), ApprovalModified},
		{"turned off", fmt.Sprintf("%s\nenabled = false\ntrusted_hash = %q\n", header, hash), ApprovalDisabled},
		{"turned off, never approved", header + "\nenabled = false\n", ApprovalDisabled},
		{"section with no hash", header + "\n", ApprovalMissing},
		{"hooks feature off", fmt.Sprintf("[features]\nhooks = false\n\n%s\ntrusted_hash = %q\n", header, hash), ApprovalHooksOff},
		{"hooks feature off, commented", fmt.Sprintf("[features]\nhooks = false # off\n\n%s\ntrusted_hash = %q\n", header, hash), ApprovalHooksOff},
		{"turned off, commented", fmt.Sprintf("%s\nenabled = false # for now\ntrusted_hash = %q\n", header, hash), ApprovalDisabled},
		{"hash with a comment", fmt.Sprintf("%s # note\ntrusted_hash = %q # note\n", header, hash), ApprovalApproved},
		{"hooks feature off, dotted", fmt.Sprintf("features.hooks = false\n%s\ntrusted_hash = %q\n", header, hash), ApprovalHooksOff},
		{"hooks feature off, inline", fmt.Sprintf("features = { apps = true, hooks = false }\n%s\ntrusted_hash = %q\n", header, hash), ApprovalHooksOff},
		{"turned off and changed", fmt.Sprintf("%s\nenabled = false\ntrusted_hash = \"sha256:other\"\n", header), ApprovalDisabled},
		{"quoted key", fmt.Sprintf("[features]\n\"hooks\" = false\n%s\ntrusted_hash = %q\n", header, hash), ApprovalHooksOff},
		{"literal string", fmt.Sprintf("%s\ntrusted_hash = '%s'\n", header, hash), ApprovalApproved},
		{"unparseable", "not toml =", ApprovalUnknown},
		{"next section ignored", fmt.Sprintf("%s\n[other]\ntrusted_hash = %q\n", header, hash), ApprovalMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := approvalOf([]handlerSlot{slot}); got != tc.want {
				t.Errorf("approval = %s, want %s", got, tc.want)
			}
		})
	}
}

// One unapproved handler is the answer even beside an approved one.
func TestApprovalOfKeepsTheWorstHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	first := handlerSlot{event: "Stop", hook: map[string]any{"type": "command", "command": "a"}, keys: []string{"k:stop:0:0"}}
	second := handlerSlot{event: "Stop", hook: map[string]any{"type": "command", "command": "b"}, keys: []string{"k:stop:1:0"}}
	hash, _ := handlerHash(first)
	config := fmt.Sprintf("[hooks.state.\"k:stop:0:0\"]\ntrusted_hash = %q\n", hash)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := approvalOf([]handlerSlot{first, second}); got != ApprovalMissing {
		t.Errorf("approval = %s, want %s", got, ApprovalMissing)
	}
}

// Approved handlers under two different path spellings do not make one approval.
func TestApprovalOfNeedsOneSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	hook := map[string]any{"type": "command", "command": "a"}
	first := handlerSlot{event: "Stop", hook: hook, keys: []string{"link:stop:0:0", "real:stop:0:0"}}
	second := handlerSlot{event: "SubagentStop", hook: hook, keys: []string{"link:subagent_stop:0:0", "real:subagent_stop:0:0"}}
	a, _ := handlerHash(first)
	b, _ := handlerHash(second)
	config := fmt.Sprintf("[hooks.state.\"link:stop:0:0\"]\ntrusted_hash = %q\n[hooks.state.\"real:subagent_stop:0:0\"]\ntrusted_hash = %q\n", a, b)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := approvalOf([]handlerSlot{first, second}); got != ApprovalMissing {
		t.Errorf("approval = %s, want %s", got, ApprovalMissing)
	}
}

// Two complete spellings that disagree leave the answer open: an old record under one
// must not read as the approval Codex uses.
func TestApprovalOfLeavesDisagreeingSpellingsOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	slot := handlerSlot{event: "Stop", hook: map[string]any{"type": "command", "command": "a"}, keys: []string{"link:stop:0:0", "real:stop:0:0"}}
	hash, _ := handlerHash(slot)
	config := fmt.Sprintf("[hooks.state.\"link:stop:0:0\"]\ntrusted_hash = %q\n[hooks.state.\"real:stop:0:0\"]\nenabled = false\n", hash)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := approvalOf([]handlerSlot{slot}); got != ApprovalUnverifiable {
		t.Errorf("approval = %s, want %s", got, ApprovalUnverifiable)
	}
}

// A spelling with some records counts: its missing handler disagrees with an approved
// spelling rather than being dropped.
func TestApprovalOfCountsAPartlyRecordedSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	hook := map[string]any{"type": "command", "command": "a"}
	first := handlerSlot{event: "Stop", hook: hook, keys: []string{"link:stop:0:0", "real:stop:0:0"}}
	second := handlerSlot{event: "SubagentStop", hook: hook, keys: []string{"link:subagent_stop:0:0", "real:subagent_stop:0:0"}}
	a, _ := handlerHash(first)
	b, _ := handlerHash(second)
	config := fmt.Sprintf("[hooks.state.\"real:stop:0:0\"]\ntrusted_hash = %q\n[hooks.state.\"real:subagent_stop:0:0\"]\ntrusted_hash = %q\n[hooks.state.\"link:stop:0:0\"]\nenabled = false\n", a, b)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := approvalOf([]handlerSlot{first, second}); got != ApprovalUnverifiable {
		t.Errorf("approval = %s, want %s", got, ApprovalUnverifiable)
	}
}

// A known-off handler is the answer beside a missing one, so its repair is printed.
func TestApprovalOfPrefersTheKnownOffHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	hook := map[string]any{"type": "command", "command": "a"}
	first := handlerSlot{event: "Stop", hook: hook, keys: []string{"k:stop:0:0"}}
	second := handlerSlot{event: "SubagentStop", hook: hook, keys: []string{"k:subagent_stop:0:0"}}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[hooks.state.\"k:stop:0:0\"]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := approvalOf([]handlerSlot{first, second}); got != ApprovalDisabled {
		t.Errorf("approval = %s, want %s", got, ApprovalDisabled)
	}
}
