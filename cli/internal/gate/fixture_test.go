package gate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
)

// A scored run against one of the planted-defect fixtures. It calls the real reviewer,
// so it is env-gated and never part of the turn-end oracle:
//
//	COMMIT_GATE_FIXTURE=sessions-py COMMIT_GATE_FIXTURE_RUNS=1 \
//	  go test ./internal/gate/ -run TestFixture -v -timeout 30m
func TestFixture(t *testing.T) {
	name := os.Getenv("COMMIT_GATE_FIXTURE")
	if name == "" {
		t.Skip("COMMIT_GATE_FIXTURE not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "hooks", "commit-review", "fixtures", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}

	// The base tree is committed and the change tree overlaid, so the reviewer reads a
	// real repo — which the cross-file class needs, its broken caller being in a file
	// the diff does not touch.
	repo := t.TempDir()
	fixtureGit(t, repo, "init", "-q", ".")
	copyTree(t, filepath.Join(dir, "base"), repo)
	fixtureGit(t, repo, "add", "-A")
	fixtureGit(t, repo, "co"+"mmit", "-qm", "base")
	copyTree(t, filepath.Join(dir, "change"), repo)
	fixtureGit(t, repo, "add", "-A")

	message, err := os.ReadFile(filepath.Join(dir, "message.txt"))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := gate.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 1
	if runs, err := strconv.Atoi(os.Getenv("COMMIT_GATE_FIXTURE_RUNS")); err == nil && runs > 0 {
		cfg.ReviewerRuns = runs
	}

	paths, ok := gate.ChangedFiles(cfg, repo, gate.DiffSpec{StagedOnly: true})
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	diff, ok := gate.GetDiff(cfg, repo, gate.DiffSpec{StagedOnly: true})
	if !ok {
		t.Fatal("GetDiff failed")
	}
	symbols := gate.ChangedSymbols(diff, paths)
	contextBlock := gate.BuildContext(cfg, repo, symbols, paths)
	t.Logf("changed files: %v", paths)
	t.Logf("diff chars: %d, symbols: %d", len(diff), len(symbols))
	t.Logf("context block: %d chars, attached: %v", len(contextBlock), attachedFiles(contextBlock))
	if contextBlock == "" {
		t.Error("no unchanged callers attached — the cross-file class is unreachable, " +
			"which makes repo access decorative")
	}

	reviewer, err := reviewers.Resolve(cfg.ReviewerProvider, reviewers.Options{Model: cfg.ReviewerModelOverride, Effort: cfg.ReviewerEffort,
		BaseURL: cfg.ReviewerAPIBaseURL, KeyEnv: cfg.ReviewerAPIKeyEnv, Tools: cfg.ReviewerAPITools})
	if err != nil {
		t.Fatal(err)
	}
	panel := gate.NewPanel(cfg, reviewer)
	prompt := gate.BuildReviewPrompt(gate.NeutralizeTags(string(message)), gate.NeutralizeTags(diff), contextBlock, gate.Addendum{})
	result := panel.Run(context.Background(), prompt, repo, gate.DiffSpec{StagedOnly: true})

	t.Logf("answered %d/%d, outcomes %v", result.Answered, cfg.ReviewerRuns,
		gate.ReviewerReport(cfg, result.Runs))
	if result.Answered == 0 {
		t.Fatalf("no reviewer answered: %s", gate.ReviewerTrouble(result.Runs))
	}
	review, findings := gate.ComposeReview(result.Reviews)
	t.Logf("finding_slots %d, shape %v", findings, gate.FindingShape(result.Reviews, paths))
	t.Logf("\n=== RAW REVIEWS (score by hand against key.md) ===\n%s", review)
}

func attachedFiles(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "--- ") && strings.Contains(line, "(unchanged;") {
			head := strings.SplitN(line, "(", 2)[0]
			out = append(out, strings.TrimSpace(strings.TrimPrefix(head, "---")))
		}
	}
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(to, 0o755); err != nil {
				t.Fatal(err)
			}
			copyTree(t, from, to)
			continue
		}
		raw, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, raw, 0o644); err != nil {
			t.Fatal(fmt.Errorf("write %s: %w", to, err))
		}
	}
}

func fixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}
