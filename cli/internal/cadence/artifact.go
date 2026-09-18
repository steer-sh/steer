package cadence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// Artifact states: where the fix a record binds stands now, against what the record
// froze when it was registered. The state is read beside the observation and never
// feeds it.
const (
	// ArtifactPresent is the entry the record names, holding the text it froze.
	ArtifactPresent = "present"
	// ArtifactChanged is the entry under the same id with different text, or a check
	// whose detector moved.
	ArtifactChanged = "changed"
	// ArtifactAbsent is a readable store that no longer holds the entry.
	ArtifactAbsent = "absent"
	// ArtifactUnreadable is a store that could not be read or parsed.
	ArtifactUnreadable = "unreadable"
	// ArtifactUnbound is a record that names no entry, so nothing can be looked up.
	ArtifactUnbound = "unbound"
	// ArtifactElsewhere is an entry in a repository other than the one read here.
	ArtifactElsewhere = "elsewhere"
	// ArtifactUnplaced is a project-scope entry whose record does not say which
	// repository's store holds it.
	ArtifactUnplaced = "unplaced"
)

// Artifact is the fix a record binds and its current state. Scope is the store for a
// guard, rule or addendum entry; Repo is the repository a project entry or a check
// belongs to.
type Artifact struct {
	ID     string `json:"id,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Repo   string `json:"repo,omitempty"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Removed is the latest removal the owning store's archive records for a
	// present or changed fix after the record's split, in archive.TimeLayout.
	Removed string `json:"removed,omitempty"`
}

// Missing reports a state that leaves the record measuring a fix that is not there
// to be read.
func (a *Artifact) Missing() bool {
	return a != nil && (a.State == ArtifactAbsent || a.State == ArtifactUnreadable)
}

// RemovalRecorded reports a fix that is in place now but was archived by a
// removal after the record's split, so the record's window may span a period
// without it.
func (a *Artifact) RemovalRecorded() bool {
	return a != nil && a.Removed != ""
}

// ArtifactSources are the readings an artifact state compares against. Addendum is
// what the commit gate reads, loaded by the caller; nil means it was not read.
type ArtifactSources struct {
	RepoRoot string
	Addendum *gate.Addendum
}

// ArtifactOf reads the current state of the fix a record binds. It is nil for a
// declined record and for a record whose tier installs nothing.
func ArtifactOf(r mine.Record, src ArtifactSources) *Artifact {
	if mine.IsRejected(r) {
		return nil
	}
	kind, ok := tier.Of(str(r["tier"]))
	if !ok {
		return nil
	}
	switch kind {
	case tier.Guard:
		return storeArtifact(r, src, kind, "hook", "hooks", hookDigest)
	case tier.Instruction:
		return storeArtifact(r, src, kind, "rule", "rules", textDigest)
	case tier.Review:
		return addendumArtifact(r, src)
	case tier.Check:
		return checkArtifact(r, src.RepoRoot)
	}
	return nil
}

// entryDigest finds one id in a manifest section and returns the digest of its text,
// the value a record froze as rule_hash.
type entryDigest func(section json.RawMessage, id string) (hash string, found bool, err error)

// hookDigest is a guard's rule digest as mine.SteerHookFacts froze it: the entry's
// own stamp, or sha256 over the rule when the entry carries none.
func hookDigest(raw json.RawMessage, id string) (string, bool, error) {
	var entries map[string]struct {
		Rule     string `json:"rule"`
		RuleHash string `json:"rule_hash"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", false, err
	}
	e, ok := entries[id]
	if !ok {
		return "", false, nil
	}
	if e.RuleHash != "" {
		return e.RuleHash, true, nil
	}
	return digest(e.Rule), true, nil
}

func textDigest(raw json.RawMessage, id string) (string, bool, error) {
	var entries map[string]struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", false, err
	}
	e, ok := entries[id]
	if !ok {
		return "", false, nil
	}
	return digest(mine.NormalizeEntryText(e.Text)), true, nil
}

// storeArtifact reads a guard or rule entry from the store its record names.
func storeArtifact(r mine.Record, src ArtifactSources, kind tier.Kind, prefix, section string, find entryDigest) *Artifact {
	a := &Artifact{ID: str(r[prefix+"_id"]), Scope: str(r[prefix+"_scope"])}
	if a.ID == "" {
		a.State = ArtifactUnbound
		return a
	}
	dir, placed := storeDir(r, src.RepoRoot, prefix, a)
	if !placed {
		return a
	}
	data, err := os.ReadFile(filepath.Join(dir, hookstore.ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		a.State = ArtifactAbsent
		return a
	}
	if err != nil {
		return unreadable(a, err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		return unreadable(a, err)
	}
	raw, ok := manifest[section]
	if !ok {
		a.State = ArtifactAbsent
		return a
	}
	hash, found, err := find(raw, a.ID)
	switch {
	case err != nil:
		return unreadable(a, err)
	case !found:
		a.State = ArtifactAbsent
	case str(r["rule_hash"]) != "" && hash != str(r["rule_hash"]):
		a.State = ArtifactChanged
	default:
		a.State = ArtifactPresent
	}
	return markRemoval(a, r, dir, kind)
}

// markRemoval sets Removed on a present or changed fix whose store archived it
// after the record's split. Both times are compared in whole seconds, because a
// record stores its split that way: a removal in the same second as the split
// predates a record registered after a restore. An archive that cannot be read
// leaves where the fix stood unknown.
func markRemoval(a *Artifact, r mine.Record, storeDir string, kind tier.Kind) *Artifact {
	if a.State != ArtifactPresent && a.State != ArtifactChanged {
		return a
	}
	entries, err := archive.ForID(storeDir, a.ID)
	if err != nil {
		return unreadable(a, fmt.Errorf("the archive could not be read: %w", err))
	}
	split := int64(mine.SplitTS(r))
	for _, e := range entries {
		if k, ok := tier.Of(e.Tier); !ok || k != kind {
			continue
		}
		if at := e.RemovedAt(); at.Unix() > split {
			a.Removed = e.Removed
		}
	}
	return a
}

// storeDir resolves the store a record's entry lives in. A project store is read
// under the repository this reading inspects, and only when the record names that
// repository; otherwise a's state says why nothing was read.
func storeDir(r mine.Record, repoRoot, prefix string, a *Artifact) (string, bool) {
	switch a.Scope {
	case steerpath.GlobalScope:
		dir, err := hookstore.Resolve(steerpath.GlobalScope)
		if err != nil {
			unreadable(a, err)
			return "", false
		}
		return dir, true
	case steerpath.ProjectScope:
		readRoot, ok := placeProject(r, repoRoot, prefix, a)
		if !ok {
			return "", false
		}
		return projectStore(readRoot), true
	}
	a.State = ArtifactUnplaced
	return "", false
}

// placeProject resolves the checkout holding a project entry's store, or says on a
// why nothing was read.
func placeProject(r mine.Record, repoRoot, prefix string, a *Artifact) (string, bool) {
	a.Repo = str(r[prefix+"_repo"])
	if a.Repo == "" {
		a.State = ArtifactUnplaced
		return "", false
	}
	readRoot, ok, err := boundRoot(repoRoot, a.Repo)
	if err != nil {
		unreadable(a, err)
		return "", false
	}
	if !ok {
		a.State = ArtifactElsewhere
		return "", false
	}
	return readRoot, true
}

// projectStore is the project store under a repository root: the neutral directory,
// or the legacy one while only it holds a manifest, the order hookstore.Resolve reads.
func projectStore(root string) string {
	neutral, _ := steerpath.HooksDir(steerpath.ProjectScope)
	legacy, _ := steerpath.LegacyHooksDir(steerpath.ProjectScope)
	if _, err := os.Stat(filepath.Join(root, neutral, hookstore.ManifestName)); err != nil {
		if _, err := os.Stat(filepath.Join(root, legacy, hookstore.ManifestName)); err == nil {
			return filepath.Join(root, legacy)
		}
	}
	return filepath.Join(root, neutral)
}

// addendumArtifact reads a review entry from what the commit gate reads: the global
// store on disk and the project store at HEAD.
func addendumArtifact(r mine.Record, src ArtifactSources) *Artifact {
	a := &Artifact{ID: str(r["addendum_id"]), Scope: str(r["addendum_scope"])}
	switch {
	case a.ID == "":
		a.State = ArtifactUnbound
		return a
	case a.Scope == steerpath.ProjectScope:
		// Strict, where a store-backed tier reads across to the bound checkout:
		// src.Addendum is the caller's HEAD read of the inspected repository, and
		// a linked worktree's HEAD is not the bound checkout's.
		a.Repo = str(r["addendum_repo"])
		switch {
		case a.Repo == "":
			a.State = ArtifactUnplaced
			return a
		case !sameRepo(src.RepoRoot, a.Repo):
			a.State = ArtifactElsewhere
			return a
		}
	case a.Scope != steerpath.GlobalScope:
		a.State = ArtifactUnplaced
		return a
	}
	if src.Addendum == nil {
		a.State, a.Detail = ArtifactUnreadable, "the commit gate's addendum was not read"
		return a
	}
	for _, e := range src.Addendum.Errors {
		if strings.Contains(e, a.Scope+" store") {
			a.State, a.Detail = ArtifactUnreadable, e
			return a
		}
	}
	a.State = ArtifactAbsent
	for _, e := range src.Addendum.Entries {
		if e.Scope != a.Scope || e.ID != a.ID {
			continue
		}
		a.State = ArtifactPresent
		if want := str(r["addendum_hash"]); want != "" && digest(e.Text) != want {
			a.State = ArtifactChanged
		}
	}
	if a.State == ArtifactAbsent {
		return a
	}
	// A project entry's archive is read on disk, beside the working tree, though
	// the gate reads the entry at HEAD.
	dir := projectStore(src.RepoRoot)
	if a.Scope == steerpath.GlobalScope {
		var err error
		if dir, err = hookstore.Resolve(steerpath.GlobalScope); err != nil {
			return unreadable(a, err)
		}
	}
	return markRemoval(a, r, dir, tier.Review)
}

// checkArtifact reads a check declaration from the repository its record binds.
func checkArtifact(r mine.Record, repoRoot string) *Artifact {
	fix, bound := mine.CheckFixOf(r)
	a := &Artifact{ID: fix.ID, Repo: fix.Repo}
	if !bound {
		a.State = ArtifactUnbound
		return a
	}
	readRoot, ok, err := boundRoot(repoRoot, fix.Repo)
	if err != nil {
		return unreadable(a, err)
	}
	if !ok {
		a.State = ArtifactElsewhere
		return a
	}
	m, err := checks.Load(readRoot)
	if errors.Is(err, checks.ErrNoManifest) {
		a.State = ArtifactAbsent
		return a
	}
	if err != nil {
		return unreadable(a, err)
	}
	for _, d := range m.Checks {
		if d.ID != fix.ID {
			continue
		}
		det, err := checks.DetectorOf(readRoot, d)
		if err != nil {
			return unreadable(a, err)
		}
		a.State = ArtifactPresent
		if det.Digest != fix.Digest {
			a.State = ArtifactChanged
		}
		return markRemoval(a, r, checks.Dir(readRoot), tier.Check)
	}
	a.State = ArtifactAbsent
	return a
}

// boundRoot picks the checkout to read for a fix bound to identity: the inspected
// root when that is the bound checkout, and the bound checkout itself when the
// inspected root is one of its linked worktrees. It never reads a worktree's own
// copy of the store. ok is false when neither relation holds, which is
// ArtifactElsewhere; err is a bound checkout git names but nothing can read.
func boundRoot(readRoot, identity string) (root string, ok bool, err error) {
	if readRoot == "" || identity == "" {
		return "", false, nil
	}
	if got, idErr := checks.RepoIdentity(readRoot); idErr == nil && got == identity {
		return readRoot, true, nil
	}
	main, linked := checks.MainCheckout(readRoot)
	if !linked || main != identity {
		return "", false, nil
	}
	// checks.Load reports a gone checkout as a missing manifest, so check the
	// directory before a caller reads that as an absent declaration.
	if _, statErr := os.Stat(main); statErr != nil {
		return "", false, fmt.Errorf("the bound checkout could not be read: %w", statErr)
	}
	return main, true, nil
}

// sameRepo compares a repository root with a stored identity, both resolved the way
// checks.RepoIdentity resolves them. No root is no repository read here.
func sameRepo(root, identity string) bool {
	if root == "" {
		return false
	}
	got, err := checks.RepoIdentity(root)
	return err == nil && got == identity
}

func unreadable(a *Artifact, err error) *Artifact {
	a.State, a.Detail = ArtifactUnreadable, err.Error()
	return a
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
