package codex

import (
	"os"
	"strings"
	"testing"
)

// bareCommit is a payload naming no workdir, which is what Codex code mode sends.
func bareCommit(cwd, command string) string {
	return `{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"s",` +
		`"cwd":"` + cwd + `","tool_input":{"command":"` + command + `"}}`
}

func TestRunRefusesACommitWhoseRepositoryThePayloadDoesNotName(t *testing.T) {
	cfg := testConfig(t)
	repo := buildRepo(t)

	out := runGate(t, cfg, bareCommit(repo, `git commit -m \"add a\"`))
	if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("output = %#v", out)
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "git -C /absolute") {
		t.Fatalf("the reason does not name the retry: %q", out.HookSpecificOutput.PermissionDecisionReason)
	}
	// No reviewer ran, so the refusal must leave the adjudication corpus untouched.
	if _, err := os.Stat(cfg.LogFile()); err == nil {
		t.Fatal("the refusal wrote an adjudication row")
	}
}

func TestRunReviewsACommitThatNamesItsRepositoryAbsolutely(t *testing.T) {
	for name, command := range map[string]string{
		"dash-C": `git -C ` + "%s" + ` commit -m \"add a\"`,
		"cd":     `cd ` + "%s" + ` && git commit -m \"add a\"`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.ReviewerRuns = 1
			repo := buildRepo(t)
			command := strings.Replace(command, "%s", repo, 1)

			out := runGate(t, cfg, bareCommit("/", command))
			if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("output = %#v", out)
			}
			if row := readRows(t, cfg)[0]; row["phase"] != "deny" {
				t.Fatalf("the reviewer did not run: %#v", row)
			}
		})
	}
}

func TestRunRefusesARelativeRepositorySelection(t *testing.T) {
	cfg := testConfig(t)
	repo := buildRepo(t)

	// The relative path resolves against the very directory the payload may have
	// dropped, so it names nothing the gate can trust.
	out := runGate(t, cfg, bareCommit(repo, `cd sub && git commit -m \"add a\"`))
	if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("output = %#v", out)
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "ABSOLUTE") {
		t.Fatalf("reason = %q", out.HookSpecificOutput.PermissionDecisionReason)
	}
	if _, err := os.Stat(cfg.LogFile()); err == nil {
		t.Fatal("the refusal wrote an adjudication row")
	}
}

// A search names no repository either, so the text must decide before the repository
// refusal tells its author to retry a commit the command does not hold.
func TestRunRefusesAPossibleCommitBeforeTheRepositoryCheck(t *testing.T) {
	cfg := testConfig(t)
	out := runGate(t, cfg, bareCommit("/tmp", `rg \"git commit\" cli/`))
	if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("output = %#v", out)
	}
	if reason := out.HookSpecificOutput.PermissionDecisionReason; !strings.Contains(reason, "possible commit") {
		t.Fatalf("reason = %q", reason)
	}
	if _, err := os.Stat(cfg.LogFile()); err == nil {
		t.Fatal("the refusal wrote an adjudication row")
	}
}

func TestRunLeavesANonCommitCommandAlone(t *testing.T) {
	cfg := testConfig(t)
	if out := runGate(t, cfg, bareCommit("/tmp", "git status")); out.HookSpecificOutput != nil {
		t.Fatalf("a read-only command was refused: %#v", out)
	}
}
