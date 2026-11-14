package cc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

type initialCommitReviewer struct{ prompts []string }

func (r *initialCommitReviewer) Name() string       { return "stub" }
func (r *initialCommitReviewer) ModelLabel() string { return "stub" }
func (r *initialCommitReviewer) Review(_ context.Context, prompt, _ string) gate.RawRun {
	r.prompts = append(r.prompts, prompt)
	return gate.RawRun{Response: "[1] SEVERITY: BLOCKING\n    FILE: selected.go\n    CLAIM: defect.\n    FAILS WHEN: called.\n", Status: "SUCCESS"}
}

// TestInitialBundledAddReachesReview checks a native request before its staging step runs.
func TestInitialBundledAddReachesReview(t *testing.T) {
	testhome.Set(t, t.TempDir())
	for _, format := range []string{"sha1", "sha256"} {
		for _, tc := range []struct {
			name   string
			add    string
			staged bool
		}{
			{"empty-index", ".", false},
			{"staged-index", ".", true},
			{"selected-path", "selected.go", true},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				run := func(args ...string) {
					t.Helper()
					cmd := exec.Command("git", args...)
					cmd.Dir = root
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git %v: %v: %s", args, err, out)
					}
				}
				init := exec.Command("git", "init", "-q", "--object-format="+format)
				init.Dir = root
				if out, err := init.CombinedOutput(); err != nil {
					t.Skipf("git cannot initialize %s: %v: %s", format, err, out)
				}
				for _, name := range []string{"selected.go", "staged.go", "unrelated.go"} {
					body := "package probe\n" + strings.Repeat("// "+name+" payload\n", 80)
					if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if tc.staged {
					run("add", "staged.go")
				}
				command := "git add " + tc.add + " && git " + c + " -m initial"
				if reason, _ := PreflightDeny(command, root); reason != "" {
					t.Fatalf("initial bundle refused before review: %s", reason)
				}
				spec := DiffSpecFor(command, root)
				cfg := gate.Defaults()
				cfg.StateDir = t.TempDir()
				cfg.ReviewerRuns = 1
				paths, ok := gate.ChangedFiles(cfg, root, spec)
				if !ok {
					t.Fatal("initial commit's file list could not be read")
				}
				want := 2
				if tc.add == "." {
					want = 3
				}
				if len(paths) != want {
					t.Fatalf("changed paths = %v, want %d", paths, want)
				}
				reviewer := &initialCommitReviewer{}
				decision := gate.NewEngine(cfg, reviewer).Handle(context.Background(), gate.Request{
					Repo: RepoDir(command, root), Actor: "initial", SessionID: "initial",
					Channel: gate.ChannelPreToolUse, Spec: spec, Message: "initial",
				})
				if decision.Kind != gate.Deny || len(reviewer.prompts) != 1 {
					t.Fatalf("decision = %+v; reviewer calls = %d, want one review and Deny", decision, len(reviewer.prompts))
				}
				prompt := reviewer.prompts[0]
				for _, name := range []string{"selected.go", "staged.go"} {
					if !strings.Contains(prompt, "+// "+name+" payload") {
						t.Errorf("reviewer did not receive %s's addition", name)
					}
				}
				if strings.Contains(prompt, "+// unrelated.go payload") != (tc.add == ".") {
					t.Error("unrelated additions must follow the bundled add's selection")
				}
			})
		}
	}
}
