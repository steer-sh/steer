package cc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestForcedAddsReviewTheFilesGitStages compares the reviewed additions with Git's index.
func TestForcedAddsReviewTheFilesGitStages(t *testing.T) {
	testhome.Set(t, t.TempDir())
	cases := []struct {
		name    string
		adds    [][]string
		command string
	}{
		{"short", [][]string{{"add", "-f", "ignored.go"}}, "git add -f ignored.go"},
		{"long", [][]string{{"add", "--force", "forced"}}, "git add --force forced"},
		{"cluster", [][]string{{"add", "-Af", "ignored.go"}}, "git add -Af ignored.go"},
		{"whole-repository", [][]string{{"add", "-Af"}}, "git add -Af"},
		{"mixed", [][]string{{"add", "-f", "forced"}, {"add", "normal"}}, "git add -f forced && git add normal"},
		{"overlapping-forced-adds", [][]string{{"add", "-f", "forced", ":(exclude)forced/skip.go"}, {"add", "-f", "forced/skip.go"}}, "git add -f forced ':(exclude)forced/skip.go' && git add -f forced/skip.go"},
		{"excluded", [][]string{{"add", "-f", "forced", ":(exclude)forced/skip.go"}}, "git add -f forced ':(exclude)forced/skip.go'"},
		{"force-disabled", [][]string{{"add", "--force", "--no-force", "."}}, "git add --force --no-force ."},
		{"literal-dash-path", [][]string{{"add", "--", "-f"}}, "git add -- -f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			run := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return string(out)
			}
			write := func(path, body string) {
				t.Helper()
				target := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			run("init", "-q")
			run("config", "core.hooksPath", os.DevNull)
			write(".gitignore", "ignored.go\nforced/hidden.go\nforced/skip.go\nnormal/hidden.go\n")
			run("add", ".gitignore")
			run(c, "-qm", "base")
			for _, path := range []string{"ignored.go", "forced/hidden.go", "forced/visible.go", "forced/skip.go", "normal/hidden.go", "normal/visible.go", "stray.go", "-f"} {
				write(path, "package probe\n"+strings.Repeat("// addition in "+path+"\n", 80))
			}
			command := tc.command + " && git " + c + " -m probe"
			if reason, _ := PreflightDeny(command, root); reason != "" {
				t.Fatal(reason)
			}
			cfg := gate.Defaults()
			cfg.StateDir = t.TempDir()
			cfg.ReviewerRuns = 1
			spec := DiffSpecFor(command, root)
			paths, ok := gate.ChangedFiles(cfg, root, spec)
			if !ok {
				t.Fatal("ChangedFiles failed")
			}
			reviewer := &initialCommitReviewer{}
			decision := gate.NewEngine(cfg, reviewer).Handle(context.Background(), gate.Request{Repo: root, Actor: "force", SessionID: "force", Spec: spec, Message: "probe"})
			if decision.Kind != gate.Deny || len(reviewer.prompts) != 1 {
				t.Fatalf("decision=%+v; reviewer calls=%d", decision, len(reviewer.prompts))
			}
			for _, args := range tc.adds {
				run(args...)
			}
			staged := strings.Fields(run("diff", "--cached", "--name-only"))
			sort.Strings(staged)
			sort.Strings(paths)
			if !reflect.DeepEqual(paths, staged) {
				t.Fatalf("reviewed paths=%v; Git stages=%v; spec=%+v", paths, staged, spec)
			}
			for _, path := range staged {
				if !strings.Contains(reviewer.prompts[0], "+// addition in "+path+"\n") {
					t.Errorf("reviewer did not see %s's content", path)
				}
			}
			for _, path := range []string{"ignored.go", "forced/hidden.go", "forced/skip.go", "normal/hidden.go", "stray.go"} {
				stagedPath := false
				for _, s := range staged {
					stagedPath = stagedPath || s == path
				}
				if !stagedPath && strings.Contains(reviewer.prompts[0], "+// addition in "+path+"\n") {
					t.Errorf("reviewer saw unselected %s", path)
				}
			}
		})
	}
}
