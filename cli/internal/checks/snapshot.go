package checks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SnapshotRefPrefix holds one ref per commit a snapshot case names, so the
// repository keeps the commit while the case needs it.
const SnapshotRefPrefix = "refs/steer/snapshots/"

// errSnapshotMissing reports a snapshot commit the repository does not hold.
var errSnapshotMissing = errors.New("snapshot unavailable")

// gitRepoEnv names the variables that point git at another repository, index
// or work tree than the one a command names.
var gitRepoEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY"}

// runGit runs git with args in dir under CaseTimeout. The inherited variables in
// gitRepoEnv are dropped and env is added.
func runGit(dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !containsString(gitRepoEnv, name) {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return out, fmt.Errorf("git %s exceeded %s", args[0], CaseTimeout)
	}
	if err != nil {
		return out, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// hasCommit reports whether the repository rooted at root holds the commit.
func hasCommit(root, sha string) bool {
	_, err := runGit(root, nil, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// retainSnapshots pins every snapshot commit the cases name and the repository
// holds. A commit it does not hold is left for replay to report.
func retainSnapshots(root string, cases CaseFile) error {
	pinned := map[string]bool{}
	for _, c := range cases.Cases {
		if c.Snapshot == "" || pinned[c.Snapshot] || !hasCommit(root, c.Snapshot) {
			continue
		}
		pinned[c.Snapshot] = true
		if _, err := runGit(root, nil, "update-ref", SnapshotRefPrefix+c.Snapshot, c.Snapshot); err != nil {
			return fmt.Errorf("retain snapshot %s: %w", c.Snapshot, err)
		}
	}
	return nil
}

// restoreSnapshot writes the commit's tree into tree through a temporary index
// in scratch, so the repository's index and work tree are only read.
func restoreSnapshot(root, sha, scratch, tree string) error {
	if !hasCommit(root, sha) {
		return errSnapshotMissing
	}
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "snapshot.index")}
	if _, err := runGit(root, index, "read-tree", sha); err != nil {
		return err
	}
	_, err := runGit(root, index, "checkout-index", "--all", "--force", "--prefix="+tree+string(filepath.Separator))
	return err
}

// applyPatch applies a patch to tree as a directory outside any repository.
func applyPatch(scratch, tree string, patch []byte) error {
	file := filepath.Join(scratch, "defect.patch")
	if err := os.WriteFile(file, patch, 0o644); err != nil {
		return err
	}
	_, err := runGit(tree, []string{"GIT_CEILING_DIRECTORIES=" + scratch}, "apply", "--whitespace=nowarn", file)
	return err
}
