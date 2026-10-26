package gate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// CommitView reads the repository as the commit under review lands it. A path the
// commit changes reads from where the diff reads it; every other path reads from the
// index, so an unstaged edit that does not land is never what a reviewer sees.
//
// It holds regular files only: a symlink or a submodule is not in the view.
type CommitView struct {
	root  string
	paths []string
	blobs map[string]string // the index object a path reads; "" reads the working tree
}

// ErrNotInView is a read of a path the commit does not land as a regular file.
var ErrNotInView = errors.New("not a file in this commit")

// NewCommitView resolves the view for spec at the work tree that holds cwd. ok=false
// means git could not answer.
func NewCommitView(cfg Config, cwd string, spec DiffSpec) (*CommitView, bool) {
	root := Toplevel(cwd)
	listed, ok := Git(root, gitMedium, "ls-files", "--stage", "--full-name", "-z")
	if !ok {
		return nil, false
	}
	blobs := map[string]string{}
	for _, entry := range nulFields(listed) {
		// "<mode> <object> <stage>\t<path>"
		meta, path, found := strings.Cut(entry, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[2] != "0" {
			continue
		}
		if fields[0] == "100644" || fields[0] == "100755" {
			blobs[path] = fields[1]
		}
	}

	// An intent-to-add entry (`git add -N`) holds an empty blob that a commit leaves
	// out; the index-to-worktree diff names it as an addition. A spec that stages it
	// puts it back below, from disk.
	intents, ok := Git(root, gitMedium, "diff", "--name-only", "--no-relative", "--no-renames", "--diff-filter=A", "-z")
	if !ok {
		return nil, false
	}
	for _, path := range nulFields(intents) {
		delete(blobs, path)
	}

	var fromDisk []string
	if !spec.StagedOnly {
		// The paths the command stages land their working copy: where it differs
		// from the index, within each add's own pathspecs, or anywhere for -a. A
		// staged path no add names keeps its index blob.
		selectors := spec.TrackedAddPathspecs
		if len(selectors) == 0 {
			selectors = [][]string{{":/"}}
		}
		for _, specs := range selectors {
			// From cwd, as trackedSelector resolves the same specs; --no-relative keeps
			// the names root-relative. --no-renames names a rename's old path too,
			// which then leaves the view.
			args := append([]string{"diff", "--name-only", "--no-relative", "--no-renames", "-z", "--"}, specs...)
			names, ok := Git(cwd, gitMedium, args...)
			if !ok {
				return nil, false
			}
			fromDisk = append(fromDisk, nulFields(names)...)
		}
	}
	if spec.IncludeUntracked {
		// From cwd, where the command's own pathspecs are relative.
		plan := &diffPlan{cfg: cfg, cwd: cwd, spec: spec, keepCruft: true}
		fromDisk = append(fromDisk, plan.untrackedPaths()...)
		if plan.incomplete {
			return nil, false
		}
	}

	v := &CommitView{root: root, blobs: blobs}
	for _, path := range fromDisk {
		if v.regularOnDisk(path) {
			blobs[path] = ""
		} else {
			// A deletion lands, and a changed path that is no longer a file is not one.
			delete(blobs, path)
		}
	}
	for path := range blobs {
		v.paths = append(v.paths, path)
	}
	sort.Strings(v.paths)
	return v, true
}

// Paths lists the view's root-relative paths in sorted order.
func (v *CommitView) Paths() []string { return v.paths }

// Has reports whether path is a file in the view.
func (v *CommitView) Has(path string) bool {
	_, ok := v.blobs[path]
	return ok
}

// Read returns path's content as the commit lands it.
func (v *CommitView) Read(path string) ([]byte, error) {
	object, ok := v.blobs[path]
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, ErrNotInView)
	}
	// By object id, so a path staged again after the view was built reads as listed.
	if object != "" {
		blob, ok := Git(v.root, gitMedium, "cat-file", "blob", object)
		if !ok {
			return nil, fmt.Errorf("no index blob for %s", path)
		}
		return []byte(blob), nil
	}
	return v.readDisk(path, -1)
}

// regularOnDisk reports whether path names a regular file inside the work tree.
func (v *CommitView) regularOnDisk(path string) bool {
	root, err := os.OpenRoot(v.root)
	if err != nil {
		return false
	}
	defer root.Close()
	return regularAt(root, path) == nil
}

// regularAt refuses a path that is not a regular file or that a symlinked parent
// reaches. os.Root follows a symlink that stays inside the root, where git would
// stage the link and delete the tracked file under it.
func regularAt(root *os.Root, path string) error {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s: %w", path, ErrNotInView)
		}
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, ErrNotInView)
	}
	return nil
}

// readDisk opens path through an os.Root, so a symlinked parent directory cannot lead
// the read out of the work tree, and refuses anything but a regular file at open. A
// limit of 0 or more reads at most limit+1 bytes, so a file that grew past it shows.
func (v *CommitView) readDisk(path string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(v.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := regularAt(root, path); err != nil {
		return nil, err
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "read", Path: path, Err: ErrNotInView}
	}
	if limit >= 0 {
		return io.ReadAll(io.LimitReader(f, limit+1))
	}
	return io.ReadAll(f)
}
