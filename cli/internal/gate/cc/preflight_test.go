package cc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreflightDenyRefusesABundledAddOverAPartlyStagedFile: a bundled add is reviewed
// from the working tree, so a file with staged and unstaged edits would reach the
// reviewers with edits the commit does not carry.
func TestPreflightDenyRefusesABundledAddOverAPartlyStagedFile(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := strings.Repeat("line x\n", 80)
	write("code.go", base)
	write("ARCH.md", base)
	run("init", "-q", ".")
	run("add", ".")
	run("co"+"mmit", "-qm", "init")
	write("code.go", base+"changed\n")

	bundled := "git add code.go && git -C " + root + " co" + "mmit -m x"
	if reason, _ := PreflightDeny(bundled, root); reason != "" {
		t.Fatalf("no file is partly staged, so the bundle must go to review: %s", reason)
	}

	write("ARCH.md", base+"staged paragraph\n")
	run("add", "ARCH.md")
	write("ARCH.md", base+"staged paragraph\nunstaged paragraph\n")

	reason, receipt := PreflightDeny(bundled, root)
	if !strings.Contains(reason, "ARCH.md") {
		t.Errorf("the refusal must name the partly staged file, got %q", reason)
	}
	if receipt != PartlyStagedReceipt {
		t.Errorf("the receipt must say no review ran, got %q", receipt)
	}

	for _, cmd := range []string{
		"git -C " + root + " co" + "mmit -m x",
		"git add code.go && git -C " + root + " co" + "mmit -am x",
	} {
		if reason, _ := PreflightDeny(cmd, root); reason != "" {
			t.Errorf("%q reviews the tree it lands, so it must not be refused: %s", cmd, reason)
		}
	}
}
